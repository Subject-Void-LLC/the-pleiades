// Release Gate for rescue: and always: through the real binary against a
// real sshd: a block task that fails for real, a rescue that answers it and
// an always that runs regardless, each proven by what it left on the device.
// Beside it, the gate for ignore_errors, changed_when and failed_when, each
// decided over what the real device did or wrote.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// rescueGateRunbook's block runs a command that fails on the device; its
// rescue writes a marker naming the failed task, read from error; its always
// writes another; the task after the block writes a third, which proves the
// run went on past the rescued failure.
const rescueGateRunbook = `# A block that fails for real, a rescue that answers it, an always that runs regardless.
# Blast radius: the release gate's own sshd container.
# Changes: three marker files under /tmp.
# Undo: nothing; the container is discarded.
id: rescue-gate
hosts: box
tasks:
  - name: guarded
    block:
      - name: fail for real
        exec.command:
          cmd: "false"
      - name: never reached
        exec.shell:
          cmd: "echo nope > /tmp/rescue-gate-unreached"
    rescue:
      - name: answer it
        exec.shell:
          cmd: "echo {{ error.task | quote }} > /tmp/rescue-gate-rescued"
    always:
      - name: clean up
        exec.shell:
          cmd: "echo done > /tmp/rescue-gate-always"
  - name: after
    exec.shell:
      cmd: "echo on > /tmp/rescue-gate-after"
`

// TestCLI_RescueAndAlwaysRunAgainstARealDevice: the rescue ran with the
// failed task's id, always ran, the task after the block ran, the block's
// second task did not; the report says rescued with exit 0, and
// --fail-on-rescue makes the same run end 4.
func TestCLI_RescueAndAlwaysRunAgainstARealDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sshd container test in short mode")
	}
	dir, home, addr := sshdGateProject(t, "rescue.yaml", rescueGateRunbook)

	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/rescue.yaml")
	if code != 0 || rep.Outcome.Status != "rescued" {
		t.Fatalf("exit %d, report:\n%s", code, raw)
	}
	var rescuedBy string
	for _, task := range rep.Tasks {
		if task.ID == "tasks[0].block[0]" {
			rescuedBy = task.RescuedBy
		}
	}
	if rescuedBy != "tasks[0].rescue[0]" {
		t.Errorf("the failed task's rescued_by is %q", rescuedBy)
	}
	for file, want := range map[string]string{
		"/tmp/rescue-gate-rescued": "tasks[0].block[0]",
		"/tmp/rescue-gate-always":  "done",
		"/tmp/rescue-gate-after":   "on",
	} {
		if got := strings.TrimSpace(verifyOverSSH(t, addr, "cat "+file)); got != want {
			t.Errorf("%s holds %q, want %q", file, got, want)
		}
	}
	if got := verifyOverSSH(t, addr, "test -e /tmp/rescue-gate-unreached && echo exists || echo absent"); strings.TrimSpace(got) != "absent" {
		t.Errorf("the block's task after the failure ran: %q", got)
	}

	rep, raw, code = runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/rescue.yaml", "--fail-on-rescue")
	if code != 4 || rep.Outcome.Status != "rescued" || rep.Outcome.ExitCode != 4 {
		t.Fatalf("--fail-on-rescue: exit %d, report:\n%s", code, raw)
	}
}

// sshdGateProject starts the release gate's sshd container and a project
// managing it as box, the way a user sets one up, with runbook written to
// runbooks/name and validated clean. It returns the project directory, the
// home whose known_hosts holds the device's key, and the device's address.
func sshdGateProject(t *testing.T, name, runbook string) (dir, home, addr string) {
	t.Helper()
	host, port := startReleaseGateContainer(t)
	addr = net.JoinHostPort(host, strconv.Itoa(port))
	home = t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "box", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "box", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", name), []byte(runbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithHome(t, dir, home, "validate", "runbooks/"+name); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	return dir, home, addr
}

// failureKeywordsRunbook decides each task's outcome over the real device:
// a command that really fails under ignore_errors, a marker the next task
// writes, two reads of that marker whose changed_when holds and does not
// hold over the stdout the device returned, and a failed_when over the same
// stdout, which fails the run before its last task.
const failureKeywordsRunbook = `# Failure keywords decided over what a real device did and wrote.
# Blast radius: the release gate's own sshd container.
# Changes: two marker files under /tmp.
# Undo: nothing; the container is discarded.
id: failure-keywords-gate
hosts: box
tasks:
  - name: fail for real
    exec.command:
      cmd: "false"
    ignore_errors: true
  - name: after the ignored failure
    exec.shell:
      cmd: "echo on > /tmp/keywords-gate-after"
  - name: read the marker, changed when it says on
    exec.command:
      cmd: "cat /tmp/keywords-gate-after"
    register: said_on
    changed_when: ["result.said_on.stdout == 'on'"]
  - name: read the marker, changed when it says off
    exec.command:
      cmd: "cat /tmp/keywords-gate-after"
    register: said_off
    changed_when: ["result.said_off.stdout == 'off'"]
  - name: fail when the marker says on
    exec.command:
      cmd: "cat /tmp/keywords-gate-after"
    register: judged
    failed_when: ["result.judged.stdout == 'on'"]
  - name: never reached
    exec.shell:
      cmd: "echo nope > /tmp/keywords-gate-unreached"
`

// TestCLI_FailureKeywordsDecideOverARealDevice: the failure ignore_errors
// let pass is recorded failed and ignored, and the marker after it exists;
// changed_when turns exec.command's own "changed" into ok when its condition
// over the real stdout does not hold, and keeps it changed when it does;
// failed_when fails a command that succeeded, the journal records that stage,
// and the task after it never runs.
func TestCLI_FailureKeywordsDecideOverARealDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sshd container test in short mode")
	}
	dir, home, addr := sshdGateProject(t, "keywords.yaml", failureKeywordsRunbook)

	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/keywords.yaml")
	if code == 0 || rep.Outcome.Status != "failed" {
		t.Fatalf("exit %d, want the run failed by failed_when:\n%s", code, raw)
	}
	byID := map[string]int{}
	for i, task := range rep.Tasks {
		byID[task.ID] = i
	}
	status := func(id string) string {
		if i, ok := byID[id]; ok {
			return rep.Tasks[i].Status
		}
		return "absent"
	}
	if i, ok := byID["tasks[0]"]; !ok || rep.Tasks[i].Status != "failed" || !rep.Tasks[i].Ignored {
		t.Errorf("the failing command under ignore_errors is not recorded failed and ignored:\n%s", raw)
	}
	for id, want := range map[string]string{
		"tasks[1]": "changed",
		"tasks[2]": "changed",
		"tasks[3]": "ok",
		"tasks[4]": "failed",
	} {
		if got := status(id); got != want {
			t.Errorf("%s is %s, want %s", id, got, want)
		}
	}
	if got := status("tasks[5]"); got == "changed" || got == "ok" {
		t.Errorf("the task after failed_when ran: %s", got)
	}
	if i, ok := byID["tasks[4]"]; ok && !strings.Contains(rep.Tasks[i].Error, "failed_when") {
		t.Errorf("the failed_when task's error does not name failed_when: %q", rep.Tasks[i].Error)
	}

	// The device itself: the marker the ignored failure did not stop, and
	// nothing from the task after failed_when.
	if got := strings.TrimSpace(verifyOverSSH(t, addr, "cat /tmp/keywords-gate-after")); got != "on" {
		t.Errorf("the marker after the ignored failure holds %q, want on", got)
	}
	if got := verifyOverSSH(t, addr, "test -e /tmp/keywords-gate-unreached && echo exists || echo absent"); strings.TrimSpace(got) != "absent" {
		t.Errorf("the task after failed_when ran on the device: %q", got)
	}

	// The journal records the failed_when task's failure as that stage, not
	// as the method's own.
	out, err := runPleiadesWithHome(t, dir, home, "journal", "show", rep.RunID, "--json")
	if err != nil {
		t.Fatalf("journal show: %v\n%s", err, out)
	}
	if !regexp.MustCompile(`"failure_stage":\s*"failed_when"`).MatchString(out) {
		t.Errorf("the journal does not record failure_stage failed_when:\n%s", out)
	}
}
