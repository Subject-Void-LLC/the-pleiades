// Package netconf: the harness for net.netconf.config's tests against a real
// NETCONF server, Netopeer2 over sysrepo in the notconf image pkg/netconf's
// conformance suite pins, reached through the method's own session (the real
// SSH subsystem, the real pkg/netconf client). notconf installs every YANG
// module under /yang-modules when it starts, so the server also carries
// assuranceModule, whose lists have two keys. The tests are in
// live_undo_container_test.go and live_check_container_test.go.
package netconf

import (
	"context"
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

// assuranceModule is a YANG module with two lists keyed by two leaves each,
// pairs and others, one per test that needs one, so no two tests share a list.
const assuranceModule = `module pleiades-assurance {
  yang-version 1.1;
  namespace "urn:pleiades:assurance";
  prefix pa;
  container pairs {
    list pair {
      key "a b";
      leaf a { type string; }
      leaf b { type string; }
      leaf note { type string; }
    }
  }
  container others {
    list pair {
      key "a b";
      leaf a { type string; }
      leaf b { type string; }
    }
  }
}
`

// assuranceNS is assuranceModule's namespace.
const assuranceNS = "urn:pleiades:assurance"

// One server for every test here, each test editing groups or lists of its
// own.
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
				Files: []testcontainers.ContainerFile{{
					Reader:            strings.NewReader(assuranceModule),
					ContainerFilePath: "/yang-modules/pleiades-assurance.yang",
					FileMode:          0o644,
				}},
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
