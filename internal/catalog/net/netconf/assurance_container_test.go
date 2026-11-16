// Package netconf: net.netconf.config against a real NETCONF server,
// Netopeer2 over sysrepo in the notconf image pkg/netconf's conformance suite
// pins, through the method's own session (the real SSH subsystem, the real
// pkg/netconf client): the reads before and after an edit, each kind of undo
// recorded and then replayed exactly as a rollback replays it (this method
// again, with the recorded parameters), a re-run reporting no change, an
// edit whose answer is lost still leaving its undo, and the check through
// the candidate datastore with its refusals.
package netconf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// assuranceImage is the notconf image pkg/netconf's conformance suite pins by
// digest, for the reason it gives there.
const assuranceImage = "ghcr.io/notconf/notconf@sha256:9ef5677e35d535ca81852d40135236e603526f4380547bc13ffc69ede1b6d03d"

// The image's own fixed test login ("echo admin:admin | chpasswd" in its
// Dockerfile): a throwaway container, never a real device.
const (
	assuranceUser     = "admin"
	assurancePassword = "admin"
)

// One server for every test here, each test editing groups of its own name.
var (
	assuranceOnce      sync.Once
	assuranceErr       error
	assuranceContainer testcontainers.Container
	assuranceHost      string
	assurancePort      int
)

// TestMain tears the shared server down once every test has run.
func TestMain(m *testing.M) {
	code := m.Run()
	if assuranceContainer != nil {
		_ = assuranceContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

// startAssurance starts the shared server once and returns its endpoint.
func startAssurance(t *testing.T) (string, int) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping net.netconf.config's container-backed suite in -short mode")
	}
	assuranceOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        assuranceImage,
				ExposedPorts: []string{"830/tcp"},
				WaitingFor: wait.ForAll(
					wait.ForLog("Listening on :::830 for SSH connections").WithStartupTimeout(2*time.Minute),
					testsupport.SSHGreeting("830/tcp"),
				),
			},
			Started: true,
		})
		if err != nil {
			_ = testcontainers.TerminateContainer(c)
			assuranceErr = fmt.Errorf("starting the NETCONF server: %w", err)
			return
		}
		assuranceContainer = c
		if assuranceHost, err = c.Host(ctx); err != nil {
			assuranceErr = err
			return
		}
		mapped, err := c.MappedPort(ctx, "830/tcp")
		if err != nil {
			assuranceErr = err
			return
		}
		assurancePort = int(mapped.Num())
	})
	if assuranceErr != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("could not start the NETCONF server (is Docker running?): %v", assuranceErr)
		}
		t.Fatal(assuranceErr)
	}
	return assuranceHost, assurancePort
}

// liveDevice is the server as a generic_netconf device.
type liveDevice struct{ *netconfDevice }

func (liveDevice) DeviceType() string { return "generic_netconf" }

// liveRC is a run's context on the command line, holding the login.
type liveRC struct{ *secretContext }

func (liveRC) StagedChecksAllowed() bool { return true }

// live returns the device and a fresh context for one call.
func live(t *testing.T) (inventory.InventoryItem, *liveRC) {
	t.Helper()
	host, port := startAssurance(t)
	device := liveDevice{&netconfDevice{Stub: &inventorytest.Stub{StubID: "nc1", StubName: "nc1", Caps: []capability.Name{capability.NameNetconf}}, host: host, port: port}}
	return device, &liveRC{&secretContext{stubContext: newStubContext(), secrets: map[string]string{wire.SecretUsername: assuranceUser, wire.SecretPassword: assurancePassword}}}
}

// call runs the method, or its check, with params and the unverified host key
// the throwaway server presents.
func call(t *testing.T, check bool, params map[string]any) (bool, *liveRC, error) {
	t.Helper()
	device, rc := live(t)
	params["insecure_skip_host_key_verify"] = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	method := Config
	if check {
		method = ConfigCheck
	}
	res, err := method(ctx, rc, device, params)
	return res.Changed, rc, err
}

// session opens a session of the test's own on target.
func session(t *testing.T, target netconf.Datastore) *netconf.Session {
	t.Helper()
	host, port := startAssurance(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).Connect(ctx, nil,
		remoteexec.Target{Host: host, Port: port}, remoteexec.PasswordAuth(assuranceUser, assurancePassword))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		t.Fatal(err)
	}
	s, err := netconf.Open(ctx, sub, netconf.Options{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// group reads the NACM group name from source, canonical, "" when absent.
func readGroup(t *testing.T, source netconf.Datastore, name string) string {
	t.Helper()
	s := session(t, netconf.Running)
	els, err := s.ReadConfig(context.Background(), source, `<nacm xmlns="`+nacmNS+`"><groups><group><name>`+name+`</name></group></groups></nacm>`)
	if err != nil {
		t.Fatal(err)
	}
	return yangdoc.CanonicalElements(els)
}

// setGroup writes group name with users on running directly, replacing it.
func setGroup(t *testing.T, name string, users ...string) {
	t.Helper()
	var u strings.Builder
	for _, user := range users {
		u.WriteString("<user-name>" + user + "</user-name>")
	}
	s := session(t, netconf.Running)
	if err := s.EditConfig(context.Background(), netconf.EditConfigRequest{DefaultOperation: netconf.DefaultOperationNone,
		Config: groups(`<group xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="replace"><name>` + name + `</name>` + u.String() + `</group>`)}); err != nil {
		t.Fatal(err)
	}
}

// dropGroup removes group name, whatever is there.
func dropGroup(t *testing.T, name string) {
	t.Helper()
	s := session(t, netconf.Running)
	_ = s.EditConfig(context.Background(), netconf.EditConfigRequest{DefaultOperation: netconf.DefaultOperationNone,
		Config: groups(`<group xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="remove"><name>` + name + `</name></group>`)})
}

// replay runs rc's recorded undo as a rollback does: this method with the
// recorded parameters, as they are.
func replay(t *testing.T, rc *liveRC) bool {
	t.Helper()
	params := map[string]any{}
	for k, v := range undoParams(t, rc.stubContext) {
		params[k] = v
	}
	changed, _, err := call(t, false, params)
	if err != nil {
		t.Fatalf("replaying the undo %v: %v", params, err)
	}
	return changed
}

func TestLive_ACreateIsUndoneByItsRecordedUndo(t *testing.T) {
	const name = "ac-create"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	content := groups(`<group><name>` + name + `</name><user-name>alice</user-name></group>`)

	changed, rc, err := call(t, false, map[string]any{paramContent: content})
	if err != nil || !changed || readGroup(t, netconf.Running, name) == "" {
		t.Fatalf("the create: changed %v, %v", changed, err)
	}
	again, rerun, err := call(t, false, map[string]any{paramContent: content})
	if err != nil || again {
		t.Fatalf("a re-run of the create: changed %v, %v", again, err)
	}
	if _, recorded := rerun.stats[sdk.StatInverse]; recorded {
		t.Errorf("a re-run that changed nothing recorded an undo: %v", rerun.stats[sdk.StatInverse])
	}
	if !replay(t, rc) || readGroup(t, netconf.Running, name) != "" {
		t.Fatalf("the undo left the group there: %s", readGroup(t, netconf.Running, name))
	}
}

func TestLive_AReplaceIsUndoneByPuttingTheEntryBack(t *testing.T) {
	const name = "ac-rep"
	setGroup(t, name, "alice", "carol")
	t.Cleanup(func() { dropGroup(t, name) })
	was := readGroup(t, netconf.Running, name)

	changed, rc, err := call(t, false, map[string]any{paramContent: groups(`<group xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0" nc:operation="replace"><name>` + name + `</name><user-name>bob</user-name></group>`)})
	if err != nil || !changed || readGroup(t, netconf.Running, name) == was {
		t.Fatalf("the replace: changed %v, %v", changed, err)
	}
	if !strings.Contains(undoParams(t, rc.stubContext)[paramContent].(string), `nc:operation="replace"`) {
		t.Fatalf("the undo %v does not put the entry back", undoParams(t, rc.stubContext))
	}
	if !replay(t, rc) || readGroup(t, netconf.Running, name) != was {
		t.Fatalf("after the undo the group reads\n%s\nwant\n%s", readGroup(t, netconf.Running, name), was)
	}
}

// TestLive_AChangeOnlyTheReadAfterCanNameIsUndone: a user merged into a group
// of one user is a changed leaf or an added leaf-list entry, which the read
// before cannot tell; the reads before and after can.
func TestLive_AChangeOnlyTheReadAfterCanNameIsUndone(t *testing.T) {
	const name = "ac-merge"
	setGroup(t, name, "alice")
	t.Cleanup(func() { dropGroup(t, name) })
	was := readGroup(t, netconf.Running, name)
	changed, rc, err := call(t, false, map[string]any{paramContent: groups(`<group><name>` + name + `</name><user-name>dave</user-name></group>`)})
	if err != nil || !changed {
		t.Fatalf("the merge: changed %v, %v", changed, err)
	}
	if !replay(t, rc) || readGroup(t, netconf.Running, name) != was {
		t.Fatalf("after the undo the group reads\n%s\nwant\n%s", readGroup(t, netconf.Running, name), was)
	}
}

// lostAnswer is a real session whose edit is applied and whose answer is
// then lost, as a dropped connection after the device applied it leaves it.
type lostAnswer struct{ netconfSession }

func (l lostAnswer) EditConfig(ctx context.Context, req netconf.EditConfigRequest) error {
	if err := l.netconfSession.EditConfig(ctx, req); err != nil {
		return err
	}
	return errors.New("netconf: reading the reply to message-id 4: the connection was lost")
}

func TestLive_ALostAnswerStillLeavesTheUndo(t *testing.T) {
	const name = "ac-lost"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	swapOpenSession(t, func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string, target netconf.Datastore) (netconfSession, error) {
		s, err := realOpenSession(ctx, rc, device, params, fqcn, target)
		if err != nil {
			return nil, err
		}
		return lostAnswer{s}, nil
	})
	_, rc, err := call(t, false, map[string]any{paramContent: groups(`<group><name>` + name + `</name></group>`)})
	if err == nil || readGroup(t, netconf.Running, name) == "" {
		t.Fatalf("the edit: %v, the group there %v", err, readGroup(t, netconf.Running, name) != "")
	}
	if kept, _ := recordedUndo(t, rc.stubContext)[sdk.InverseKeepOnFailureKey].(bool); !kept {
		t.Fatalf("no undo stands for the failed task: %v", rc.stats)
	}
	openSession = realOpenSession
	if !replay(t, rc) || readGroup(t, netconf.Running, name) != "" {
		t.Fatal("the undo left the group there")
	}
}

func TestLive_TheCheckValidatesThroughTheCandidateAndChangesNothing(t *testing.T) {
	const name = "ac-check"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	content := groups(`<group><name>` + name + `</name><user-name>erin</user-name></group>`)
	changed, rc, err := call(t, true, map[string]any{paramContent: content, paramCandidateCheck: true})
	if err != nil || !changed || rc.stats[statCandidateCheck] != checkProved {
		t.Fatalf("the check: changed %v, %v, stats %v", changed, err, rc.stats)
	}
	if readGroup(t, netconf.Running, name) != "" || readGroup(t, netconf.Candidate, name) != "" {
		t.Fatal("the check left its edit in running or the candidate")
	}
	setGroup(t, name, "erin")
	if changed, _, err := call(t, true, map[string]any{paramContent: content, paramCandidateCheck: true}); err != nil || changed {
		t.Fatalf("a check of what is there: changed %v, %v", changed, err)
	}
	// The device's refusal is the check's answer: a real run would fail.
	if _, _, err := call(t, true, map[string]any{paramContent: groups(`<group><name>` + name + `</name><user-name>` + strings.Repeat("x", 3) + `</user-name><nonesuch/></group>`), paramCandidateCheck: true}); err == nil || !strings.Contains(err.Error(), "a real run would fail") {
		t.Fatalf("a document the device refuses: %v", err)
	}
	if readGroup(t, netconf.Candidate, name) != readGroup(t, netconf.Running, name) {
		t.Fatal("a refused check left the candidate different from running")
	}
}

func TestLive_TheCheckRefusesACandidateItCannotHoldAlone(t *testing.T) {
	const name = "ac-dirty"
	dropGroup(t, name)
	t.Cleanup(func() { dropGroup(t, name) })
	params := func() map[string]any {
		return map[string]any{paramContent: groups(`<group><name>` + name + `</name></group>`), paramCandidateCheck: true}
	}

	other := session(t, netconf.Candidate)
	staged := groups(`<group><name>` + name + `</name><user-name>someone-else</user-name></group>`)
	if err := other.EditConfig(context.Background(), netconf.EditConfigRequest{Config: staged}); err != nil {
		t.Fatal(err)
	}
	_, _, err := call(t, true, params())
	if !cannotCheck(err, "refused the candidate's lock") {
		t.Fatalf("a check of a dirty candidate = %v, want it unchecked", err)
	}
	if !strings.Contains(readGroup(t, netconf.Candidate, name), "someone-else") {
		t.Fatal("the check discarded another session's staged change")
	}
	if err := other.DiscardChanges(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := other.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _, err = call(t, true, params())
	if !cannotCheck(err, "locked by session "+other.SessionID()) {
		t.Fatalf("a check of a locked candidate = %v", err)
	}
	_ = other.Unlock(context.Background())
}
