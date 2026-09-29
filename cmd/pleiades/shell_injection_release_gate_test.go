// Adversarial gate for Phase 117a's rendered parameters: data from a
// ticket cannot become a second shell command, through the real binary
// against a real sshd.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// TestRenderedShellCommandCannotBeInjected: exec.shell's cmd is command
// text, so a runbook rendering data into it unquoted is refused by
// `pleiades validate` and by `pleiades run` before anything reaches the
// device, and with | quote the device's shell sees the data as one literal
// argument: "a; echo pwned" is printed, never run.
func TestRenderedShellCommandCannotBeInjected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the shell injection gate container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "box", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "box", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	const payload = "a; echo pwned"
	writeFile(t, dir, "runbooks/unquoted.yaml", "id: unquoted\nhosts: box\ntasks:\n  - name: echo the ticket\n    exec.shell:\n      cmd: \"printf %s {{ vars.note }}\"\n")
	writeFile(t, dir, "runbooks/quoted.yaml", "id: quoted\nhosts: box\ntasks:\n  - name: echo the ticket\n    exec.shell:\n      cmd: \"printf %s {{ vars.note | quote }}\"\n    register: echoed\n")

	for _, cmd := range [][]string{{"validate", "runbooks/unquoted.yaml"}, {"run", "runbooks/unquoted.yaml", "-e", "note=" + payload}} {
		out, err := runPleiadesWithEnv(t, dir, env, cmd...)
		if err == nil || !strings.Contains(out, "command text") {
			t.Fatalf("%v: err = %v, want the command text refusal\n%s", cmd, err, out)
		}
		if strings.Contains(out, "pwned\n") {
			t.Fatalf("%v: the injected command ran\n%s", cmd, out)
		}
	}

	rep, out, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/quoted.yaml", "-e", "note="+payload)
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("the quoted run: exit %d, %d tasks\n%s", code, len(rep.Tasks), out)
	}
	if got := rep.Tasks[0].Stats["stdout"]; got != payload {
		t.Errorf("the device printed %q, want the data as one literal argument %q", got, payload)
	}
}
