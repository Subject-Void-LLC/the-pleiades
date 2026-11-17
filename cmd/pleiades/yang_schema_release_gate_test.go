// Release gate for net.yang.schema through the real binary: a generic_netconf
// device for Netopeer2 (the notconf image the NETCONF gates pin, with two
// modules of the test's own installed) is onboarded, a check predicts the two
// files and writes neither, a run saves both through the command line's own
// project writer, and a second run changes nothing. The probe's words a
// configuration masker takes for secrets are saved as the server sent them,
// held byte for byte to rousette's /yang/ answer for the same module (the
// same server, a path the method never takes); a value the project keeps as a
// named secret, written into the other module, is masked in its file and in
// nothing the report prints; and the report holds no source text.
package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// yangGateProbe is a module whose words a configuration masker reads as
// secrets: a leaf named password, a list's key, secret before a value, an
// address.
const yangGateProbe = `module pleiades-gate-probe {
  yang-version 1.1;
  namespace "urn:pleiades:gate-probe";
  prefix pgp;
  revision 2026-11-16 {
    description "A probe with a password leaf, a secret and 10.1.2.3.";
  }
  container users {
    list user {
      key "name";
      leaf name { type string; }
      leaf password { type string; description "the secret key 100"; }
    }
  }
}
`

// yangGateSecret is the value the project keeps as a named secret, and
// yangGateSecretModule a module whose text holds it.
const (
	yangGateSecret       = "pleiades-gate-named-secret-42"
	yangGateSecretModule = `module pleiades-gate-secret {
  yang-version 1.1;
  namespace "urn:pleiades:gate-secret";
  prefix pgs;
  revision 2026-11-16 {
    description "Holds ` + yangGateSecret + `.";
  }
  leaf note { type string; }
}
`
)

// yangGateRunbook saves both modules' sources.
const yangGateRunbook = `# Saves the YANG source of two of nc1's modules into this project.
# Blast radius: files under yang/nc1 in this project; nothing on nc1 changes.
# Changes: the two .yang files. Undo: none, the project is under version
# control.
id: yang-schema
hosts: nc1
tasks:
  - name: Save the gate modules' schema
    net.yang.schema:
      modules:
        - pleiades-gate-probe
        - pleiades-gate-secret@2026-11-16
`

func TestGenericReleaseGate_YANGSchemaSavedAsServed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the net.yang.schema release gate, which runs a real NETCONF server, in short mode")
	}
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		Image:        gateNotconfImage,
		ExposedPorts: []string{"80/tcp", "830/tcp"},
		Files: []testcontainers.ContainerFile{
			{Reader: strings.NewReader(yangGateProbe), ContainerFilePath: "/yang-modules/pleiades-gate-probe.yang", FileMode: 0o644},
			{Reader: strings.NewReader(yangGateSecretModule), ContainerFilePath: "/yang-modules/pleiades-gate-secret.yang", FileMode: 0o644},
		},
		WaitingFor: wait.ForAll(
			wait.ForLog("Listening on :::830 for SSH connections").WithStartupTimeout(2*time.Minute),
			testsupport.SSHGreeting("830/tcp"),
			wait.ForHTTP("/.well-known/host-meta").WithPort("80/tcp").WithStartupTimeout(2*time.Minute),
		),
	}, Started: true})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		t.Fatalf("starting %s: %v", gateNotconfImage, err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ncPort, err := c.MappedPort(ctx, "830/tcp")
	if err != nil {
		t.Fatal(err)
	}
	webPort, err := c.MappedPort(ctx, "80/tcp")
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort(host, strconv.Itoa(int(ncPort.Num())))
	env := map[string]string{remoteexec.KnownHostsEnv: trustHostKey(t, addr)}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "nc1", "--type", "generic_netconf", "--set", "host=" + host, "--set", "netconf_port=" + strconv.Itoa(int(ncPort.Num()))},
		{"add-credential", "nc1", "--username", "admin", "--password", gateNotconfPassword},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, yangGateSecret+"\n", "add-secret", "gate", "--field", "value", "--value-stdin"); err != nil {
		t.Fatalf("add-secret: %v\n%s", err, out)
	}
	onboardJSON(t, dir, env, "nc1")
	writeFile(t, dir, "runbooks/yang.yaml", yangGateRunbook)

	// The check fetches both sources and writes neither.
	check, checkOut := yangGateRun(t, dir, env, "--mode", "check")
	if check.Outcome.ExitCode != 0 || len(check.Tasks) != 1 || check.Tasks[0].Status != "would_change" {
		t.Fatalf("the check %+v, want it checked and predicting a change\n%s", check, checkOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "yang")); !os.IsNotExist(err) {
		t.Fatalf("the check wrote into the project: %v", err)
	}

	run, runOut := yangGateRun(t, dir, env)
	if run.Outcome.ExitCode != 0 || len(run.Tasks) != 1 || run.Tasks[0].Status != "changed" {
		t.Fatalf("the run %+v\n%s", run, runOut)
	}
	probe := readProjectFile(t, dir, "yang/nc1/pleiades-gate-probe@2026-11-16.yang")
	if want := yangGateServed(t, host, int(webPort.Num()), "pleiades-gate-probe@2026-11-16"); probe != want {
		t.Errorf("the saved probe is not the server's text byte for byte:\n%s\nwant\n%s", probe, want)
	}
	for _, word := range []string{"leaf password", `key "name"`, "a secret and 10.1.2.3", "the secret key 100"} {
		if !strings.Contains(probe, word) {
			t.Errorf("the saved probe lacks %q", word)
		}
	}
	secret := readProjectFile(t, dir, "yang/nc1/pleiades-gate-secret@2026-11-16.yang")
	served := yangGateServed(t, host, int(webPort.Num()), "pleiades-gate-secret@2026-11-16")
	if !strings.Contains(served, yangGateSecret) || strings.Contains(secret, yangGateSecret) || secret != strings.ReplaceAll(served, yangGateSecret, "pleiades-masked-secret") {
		t.Errorf("the saved secret module:\n%s\nwant the server's text with the named secret replaced by pleiades-masked-secret and nothing else changed", secret)
	}
	for _, out := range []string{checkOut, runOut} {
		if strings.Contains(out, yangGateSecret) || strings.Contains(out, "leaf password") {
			t.Errorf("the report holds a source's text or the named secret:\n%s", out)
		}
	}

	again, againOut := yangGateRun(t, dir, env)
	if again.Outcome.ExitCode != 0 || len(again.Tasks) != 1 || again.Tasks[0].Status == "changed" {
		t.Fatalf("a second run %+v, want it unchanged\n%s", again, againOut)
	}
}

// yangGateRun runs the gate's runbook with --json and extra, returning the
// report and what the binary printed on standard output.
func yangGateRun(t *testing.T, dir string, env map[string]string, extra ...string) (gateReport, string) {
	t.Helper()
	args := append([]string{"run", "runbooks/yang.yaml", "--json"}, extra...)
	cmd := exec.Command(binPath, args...) // #nosec G204 -- the test's own binary
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
		t.Fatalf("pleiades %v printed no report: %v\nstdout: %s\nstderr: %s", args, err, out, stderr.String())
	}
	return rep, string(out) + stderr.String()
}

// readProjectFile is the file name names under dir.
func readProjectFile(t *testing.T, dir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// yangGateServed is rousette's /yang/ answer for key, read by the test's own
// client.
func yangGateServed(t *testing.T, host string, port int, key string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(host, strconv.Itoa(port))+"/yang/"+key+".yang", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth("admin", gateNotconfPassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("rousette's /yang/%s.yang: %d, %v", key, resp.StatusCode, err)
	}
	return string(body)
}
