// Release gate for Phase 127a on the command line: a capability an
// external program defines is granted by its probe when the device
// answers, used by the program's method, revoked and asserted by hand, and
// gone with the program.
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

// TestCLI_ExternalCapabilityIsProbedGrantedAndUsed is the release gate.
func TestCLI_ExternalCapabilityIsProbedGrantedAndUsed(t *testing.T) {
	if testing.Short() {
		t.Skip("starts an sshd container")
	}
	collections := buildExampleCollection(t)
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	homeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(homeDir, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, ".ssh", "known_hosts"), []byte(knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": homeDir, "PLEIADES_COLLECTIONS_DIR": collections}
	dir := t.TempDir()
	pleiades := func(args ...string) (string, error) { return runPleiadesWithEnv(t, dir, env, args...) }
	must := func(args ...string) string {
		t.Helper()
		out, err := pleiades(args...)
		if err != nil {
			t.Fatalf("pleiades %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	must("init")
	must("add-host", "container1", "--type", "linux_server", "--set", "host="+host, "--set", "port="+strconv.Itoa(port))
	must("add-credential", "container1", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword)
	must("collection", "approve", "note", "--yes")

	if out := must("doc", "example.notebook"); !strings.Contains(out, "defined by: note") || !strings.Contains(out, "its probe example.notebook.probe") {
		t.Errorf("doc of the capability:\n%s", out)
	}
	runbook := "# What this does: writes a note in container1's notebook.\n# Blast radius: one file in the notebook.\n" +
		"# Changes: the note.\n# Undo: remove the note by hand; the method keeps no copy.\n" +
		"id: notebook\nhosts: container1\ntasks:\n  - name: A note in the notebook\n    example.notebook.write:\n      name: hello\n      content: from a capability no core release defined\n"
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "notebook.yaml"), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}

	// No notebook yet: the probe grants nothing, and validate refuses,
	// naming the capability.
	must("onboard", "container1")
	if out, err := pleiades("validate", "runbooks/notebook.yaml"); err == nil || !strings.Contains(out, "example.notebook") {
		t.Fatalf("validate without the grant: %v\n%s", err, out)
	}

	// The notebook made: onboarding's probe grants it, and the method
	// finds the directory in the grant.
	notes := strings.TrimSpace(verifyOverSSH(t, addr, `mkdir -p "$HOME/notes" && cd "$HOME/notes" && pwd`))
	must("onboard", "container1")
	must("validate", "runbooks/notebook.yaml")
	must("run", "runbooks/notebook.yaml")
	if got := strings.TrimSpace(verifyOverSSH(t, addr, "cat "+notes+"/hello")); got != "from a capability no core release defined" {
		t.Fatalf("the note holds %q", got)
	}

	// Revoked, it is gone; asserted by hand, with a reason, it is back.
	must("revoke-capability", "container1", "example.notebook")
	if _, err := pleiades("validate", "runbooks/notebook.yaml"); err == nil {
		t.Fatal("validate passed after the grant was revoked")
	}
	if out, err := pleiades("grant-capability", "container1", "example.notebook", "--set", "dir="+notes); err == nil {
		t.Fatalf("an assertion with no reason was accepted:\n%s", out)
	}
	if out, err := pleiades("grant-capability", "container1", "POSIXFileSystemCapable", "--reason", "x"); err == nil {
		t.Fatalf("a compiled capability was asserted:\n%s", out)
	}
	must("grant-capability", "container1", "example.notebook", "--set", "dir="+notes, "--reason", "the release gate made the directory")
	must("validate", "runbooks/notebook.yaml")
	if out := must("run", "runbooks/notebook.yaml"); !strings.Contains(out, "ok") {
		t.Errorf("a converged run:\n%s", out)
	}

	// The program revoked, or gone: its method and capability go with it.
	must("collection", "revoke", "note")
	if _, err := pleiades("validate", "runbooks/notebook.yaml"); err == nil {
		t.Error("validate passed with the program's approval revoked")
	}
	gone := map[string]string{"HOME": homeDir, "PLEIADES_COLLECTIONS_DIR": ""}
	if _, err := runPleiadesWithEnv(t, dir, gone, "validate", "runbooks/notebook.yaml"); err == nil {
		t.Error("validate passed with no program loaded")
	}
}
