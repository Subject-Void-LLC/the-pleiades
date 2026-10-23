// Release Gate for a block's rollback_on_failure through the real binary
// against a real sshd: two changes the block made are undone on the device
// when its third task fails for real, before the rescue runs, and a later
// `pleiades rollback` of the run finds them already undone, saying why in
// its --json too; `pleiades journal` says the run undid its own changes.
package main_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// rollbackOnFailureGateRunbook makes two directories, then fails for real
// making one under /proc; the rescue records which of the two still exist
// when it starts, and the task after the block shows the run went on.
const rollbackOnFailureGateRunbook = `# A block that undoes its own changes when a task of it fails, before its rescue.
# Blast radius: the release gate's own sshd container.
# Changes: two directories under /tmp, undone in the run; two marker files.
# Undo: the block undoes its directories itself; nothing undoes the markers.
id: rollback-on-failure-gate
hosts: box
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: first
        file.directory:
          path: /tmp/rof-gate-one
      - name: second
        file.directory:
          path: /tmp/rof-gate-two
      - name: fail for real
        file.directory:
          path: /proc/rof-gate-impossible
    rescue:
      - name: see what is left
        exec.shell:
          cmd: "for d in /tmp/rof-gate-one /tmp/rof-gate-two; do test -e $d && echo $d; done > /tmp/rof-gate-seen; true"
  - name: after
    exec.shell:
      cmd: "echo on > /tmp/rof-gate-after"
`

// TestCLI_RollbackOnFailureUndoesTheBlockBeforeTheRescue: both directories
// are gone, and were gone when the rescue started; the run is rescued; the
// report names each undo and the task it undid; and a rollback of the run,
// leaving the two shell tasks nothing undoes, refuses because what is left
// was already undone by the run itself.
func TestCLI_RollbackOnFailureUndoesTheBlockBeforeTheRescue(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "rof.yaml"), []byte(rollbackOnFailureGateRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithHome(t, dir, home, "validate", "runbooks/rof.yaml"); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate: %v\n%s", err, out)
	}

	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/rof.yaml")
	if code != 0 || rep.Outcome.Status != "rescued" || rep.RunID == "" {
		t.Fatalf("exit %d, report:\n%s", code, raw)
	}
	undid := map[string]string{}
	for _, task := range rep.Tasks {
		if task.Undoes != "" {
			undid[task.ID] = task.Undoes + " " + task.Status
		}
	}
	if undid["tasks[0].block[1].undo[0]"] != "tasks[0].block[1] changed" || undid["tasks[0].block[0].undo[0]"] != "tasks[0].block[0] changed" {
		t.Errorf("undo tasks %v, want both directories undone", undid)
	}
	for _, path := range []string{"/tmp/rof-gate-one", "/tmp/rof-gate-two"} {
		if got := strings.TrimSpace(verifyOverSSH(t, addr, "test -e "+path+" && echo exists || echo absent")); got != "absent" {
			t.Errorf("%s is %s after the run", path, got)
		}
	}
	if seen := strings.TrimSpace(verifyOverSSH(t, addr, "cat /tmp/rof-gate-seen")); seen != "" {
		t.Errorf("the rescue found %q still there when it started", seen)
	}
	if got := strings.TrimSpace(verifyOverSSH(t, addr, "cat /tmp/rof-gate-after")); got != "on" {
		t.Errorf("the task after the block left %q", got)
	}

	rollbackArgs := []string{"rollback", rep.RunID, "--leave", "tasks[0].rescue[0]", "--leave", "tasks[1]", "--allow-unknown", "tasks[0].block[2]"}
	out, err := runPleiadesWithHome(t, dir, home, rollbackArgs...)
	if err == nil || !strings.Contains(out, "already been undone, by the run itself") {
		t.Errorf("rollback of the run: %v\n%s", err, out)
	}

	// The refusal's --json says why, as the text view does: each problem in
	// rollback.problems, and its reason in the outcome's message too.
	refused, raw, code := runPleiadesJSONWithHome(t, dir, home, append(rollbackArgs, "--mode", "check")...)
	var plan struct {
		Problems []struct {
			Reason string `json:"reason"`
		} `json:"problems"`
	}
	if err := json.Unmarshal(refused.Rollback, &plan); err != nil || code != 1 || refused.Outcome.Status != "invalid" ||
		len(plan.Problems) != 1 || !strings.Contains(plan.Problems[0].Reason, "already been undone, by the run itself") ||
		!strings.Contains(refused.Outcome.Message, "already been undone, by the run itself") {
		t.Errorf("rollback --json refusal (%v), exit %d:\n%s", err, code, raw)
	}

	// The journal says the run undid its own changes, and shows no undo
	// under an undo step: a rollback never undoes an undo.
	out, err = runPleiadesWithHome(t, dir, home, "journal", "list")
	if err != nil || !strings.Contains(out, rep.RunID) || !strings.Contains(out, "undid its own change in the run (2 undo step(s), rollback_on_failure)") {
		t.Errorf("journal list: %v\n%s", err, out)
	}
	out, err = runPleiadesWithHome(t, dir, home, "journal", "show", rep.RunID)
	if err != nil || !strings.Contains(out, "undoes: tasks[0].block[0], inside this run (rollback_on_failure)") {
		t.Errorf("journal show: %v\n%s", err, out)
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.Contains(line, ".undo[") && i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "undo:") {
			t.Errorf("journal show lists an undo under the undo step %q:\n%s", line, out)
		}
	}
}
