// Release gate for net.netconf.config's change detection, undo and check,
// through the real binary: a generic_netconf device for Netopeer2 (the notconf
// image the NETCONF gates pin) is onboarded, a runbook creates a NACM group,
// a re-run of it reports no change, a check of another group through the
// candidate datastore validates it and changes nothing, and pleiades rollback
// deletes the group again from the undo the run journal kept.
package main_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// netconfGateRunbook creates the group gate-nc on nc1.
const netconfGateRunbook = `# Creates the NACM group gate-nc on nc1 over NETCONF.
# Blast radius: nc1's access-control configuration, one group. Changes: the
# group is created. Undo: pleiades rollback deletes it, from the undo the run
# records.
id: netconf-create
hosts: nc1
tasks:
  - name: Make the gate group
    net.netconf.config:
      content: '<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><groups><group><name>gate-nc</name><user-name>erin</user-name></group></groups></nacm>'
`

// netconfCheckRunbook checks creating the group gate-nc-check on nc1 through
// the candidate datastore.
const netconfCheckRunbook = `# Checks creating the NACM group gate-nc-check on nc1, through nc1's candidate
# datastore. Blast radius: nc1's candidate datastore, locked for the check and
# proven as it was; running is never written. Changes: none. Undo: none.
id: netconf-check
hosts: nc1
tasks:
  - name: Check the gate group
    net.netconf.config:
      candidate_check: true
      content: '<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><groups><group><name>gate-nc-check</name></group></groups></nacm>'
`

// netconfGateReport runs the binary with --json in dir, with env set, and
// decodes its report.
func netconfGateReport(t *testing.T, dir string, env map[string]string, args ...string) gateReport {
	t.Helper()
	cmd := exec.Command(binPath, append(args, "--json")...) // #nosec G204 -- the test's own binary
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, _ := cmd.Output()
	var rep gateReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("pleiades %v --json printed no report: %v\nstdout: %s\nstderr: %s", args, err, out, stderr.String())
	}
	return rep
}

// netconfGateGroup reads group name from source over a session of the
// test's own, canonical, "" when it is not there.
func netconfGateGroup(t *testing.T, addr, knownHosts string, source netconf.Datastore, name string) string {
	t.Helper()
	host, portText, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	conn, err := remoteexec.New(remoteexec.Options{KnownHostsPath: knownHosts}).Connect(ctx, nil,
		remoteexec.Target{Host: host, Port: port}, remoteexec.PasswordAuth("admin", gateNotconfPassword))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		t.Fatal(err)
	}
	s, err := netconf.Open(ctx, sub, netconf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(context.Background()) }()
	els, err := s.ReadConfig(ctx, source, `<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><groups><group><name>`+name+`</name></group></groups></nacm>`)
	if err != nil {
		t.Fatal(err)
	}
	return yangdoc.CanonicalElements(els)
}

func TestGenericReleaseGate_NETCONFChangeUndoAndCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the NETCONF undo and check release gate, which runs a real NETCONF server, in short mode")
	}
	host, port := startGateContainer(t, testcontainers.ContainerRequest{
		Image:        gateNotconfImage,
		ExposedPorts: []string{"830/tcp"},
		WaitingFor: wait.ForAll(
			wait.ForLog("Listening on :::830 for SSH connections").WithStartupTimeout(2*time.Minute),
			testsupport.SSHGreeting("830/tcp"),
		),
	}, "830/tcp")
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	knownHosts := trustHostKey(t, addr)
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "nc1", "--type", "generic_netconf", "--set", "host=" + host, "--set", "netconf_port=" + strconv.Itoa(port)},
		{"add-credential", "nc1", "--username", "admin", "--password", gateNotconfPassword},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboardJSON(t, dir, env, "nc1")
	writeFile(t, dir, "runbooks/create.yaml", netconfGateRunbook)
	writeFile(t, dir, "runbooks/check.yaml", netconfCheckRunbook)

	run := netconfGateReport(t, dir, env, "run", "runbooks/create.yaml")
	if run.Outcome.ExitCode != 0 || run.RunID == "" || len(run.Tasks) != 1 || run.Tasks[0].Status != "changed" || netconfGateGroup(t, addr, knownHosts, netconf.Running, "gate-nc") == "" {
		t.Fatalf("the create run %+v", run)
	}
	again := netconfGateReport(t, dir, env, "run", "runbooks/create.yaml")
	if again.Outcome.ExitCode != 0 || len(again.Tasks) != 1 || again.Tasks[0].Status == "changed" {
		t.Fatalf("a re-run of the create %+v, want it unchanged", again)
	}

	check := netconfGateReport(t, dir, env, "run", "runbooks/check.yaml", "--mode", "check")
	if check.Outcome.ExitCode != 0 || len(check.Tasks) != 1 || check.Tasks[0].Status != "would_change" {
		t.Fatalf("the check %+v, want it checked and predicting a change", check)
	}
	if netconfGateGroup(t, addr, knownHosts, netconf.Running, "gate-nc-check") != "" || netconfGateGroup(t, addr, knownHosts, netconf.Candidate, "gate-nc-check") != "" {
		t.Fatal("the check left its group in running or the candidate")
	}

	rollback := netconfGateReport(t, dir, env, "rollback", run.RunID)
	if rollback.Outcome.ExitCode != 0 || len(rollback.methods()) != 1 || !strings.HasPrefix(rollback.methods()[0], "net.netconf.config ") {
		t.Fatalf("the rollback %+v, want one net.netconf.config step", rollback)
	}
	if netconfGateGroup(t, addr, knownHosts, netconf.Running, "gate-nc") != "" {
		t.Error("the group is still there after the rollback")
	}
}
