// Release Gate for a later rollback of a templated rollback: step through the
// real binary against a real sshd: the step reads a variable a later set_fact
// changes, the run finishes, and `pleiades rollback <run-id>` undoes what the
// run made with the value the run rendered and journaled, read back on the
// device. A templated step whose values the journal could not keep is refused
// by name before anything runs, and --leave accepts it.
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

// renderedRollbackGateRunbook makes a directory named by a set_fact, whose
// rollback: step removes the directory the same variable names, then changes
// the variable; a shell task's templated undo cannot be journaled, since
// exec.command declares no undo.
const renderedRollbackGateRunbook = `# Makes a directory whose undo reads a variable a later set_fact changes.
# Blast radius: the release gate's own sshd container.
# Changes: /tmp/rr-gate-first, and the marker /tmp/rr-gate-mark.
# Undo: pleiades rollback removes the directory; the marker is left by hand.
id: rendered-rollback-gate
hosts: box
tasks:
  - name: pick
    set_fact:
      dest: /tmp/rr-gate-first
  - name: make it
    file.directory:
      path: "{{ vars.dest }}"
    rollback:
      - name: remove it
        file.remove:
          path: "{{ vars.dest }}"
  - name: mark
    exec.command:
      cmd: "touch /tmp/rr-gate-mark"
    rollback:
      - name: unmark
        exec.command:
          cmd: "rm -f {{ vars.dest | quote }}"
  - name: pick again
    set_fact:
      dest: /tmp/rr-gate-second
`

// TestCLI_RollbackReplaysATemplatedStepAsTheRunRenderedIt: after the run,
// a directory at the second value exists too (made out of band, as a decoy a
// step rendered again would remove). The rollback refuses the shell task's
// step by name and changes nothing; with --leave for it, the rollback removes
// the first directory, the one the run made, and leaves the decoy.
func TestCLI_RollbackReplaysATemplatedStepAsTheRunRenderedIt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sshd container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "box", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "box", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "rr.yaml"), []byte(renderedRollbackGateRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithHome(t, dir, home, "validate", "runbooks/rr.yaml"); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/rr.yaml")
	if code != 0 || rep.RunID == "" {
		t.Fatalf("exit %d, report:\n%s", code, raw)
	}
	exists := func(path string) bool {
		return strings.TrimSpace(verifyOverSSH(t, addr, "test -e "+path+" && echo yes || echo no")) == "yes"
	}
	if !exists("/tmp/rr-gate-first") {
		t.Fatal("the run did not make /tmp/rr-gate-first")
	}
	verifyOverSSH(t, addr, "mkdir -p /tmp/rr-gate-second")
	// The journal shows each templated step as the run rendered it, or why a
	// rollback will refuse it.
	shown, err := runPleiadesWithHome(t, dir, home, "journal", "show", rep.RunID)
	if err != nil || !strings.Contains(shown, "step 1: file.remove path=/tmp/rr-gate-first (as the run rendered it)") ||
		!strings.Contains(shown, "step 1: exec.command (not kept: its method is not an undo the task's method declares") {
		t.Errorf("journal show: %v\n%s", err, shown)
	}

	out, err := runPleiadesWithHome(t, dir, home, "rollback", rep.RunID)
	if err == nil || !strings.Contains(out, "tasks[2]") || !strings.Contains(out, "its method is not an undo the task's method declares") ||
		!strings.Contains(out, "by hand") || !strings.Contains(out, "--leave tasks[2]") {
		t.Fatalf("rollback with the shell task's step: %v\n%s", err, out)
	}
	if !exists("/tmp/rr-gate-first") || !exists("/tmp/rr-gate-mark") {
		t.Fatal("a refused rollback changed the device")
	}

	out, err = runPleiadesWithHome(t, dir, home, "rollback", rep.RunID, "--leave", "tasks[2]")
	if err != nil {
		t.Fatalf("rollback --leave tasks[2]: %v\n%s", err, out)
	}
	if exists("/tmp/rr-gate-first") {
		t.Error("the rollback left /tmp/rr-gate-first, the directory the run made")
	}
	if !exists("/tmp/rr-gate-second") {
		t.Error("the rollback removed /tmp/rr-gate-second, which the run never made: the step was rendered with the later value")
	}
	if !exists("/tmp/rr-gate-mark") {
		t.Error("the rollback removed the marker it was told to leave")
	}
}
