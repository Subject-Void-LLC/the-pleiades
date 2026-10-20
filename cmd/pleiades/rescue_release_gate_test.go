// Release Gate for rescue: and always: through the real binary against a
// real sshd: a block task that fails for real, a rescue that answers it and
// an always that runs regardless, each proven by what it left on the device.
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
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "rescue.yaml"), []byte(rescueGateRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithHome(t, dir, home, "validate", "runbooks/rescue.yaml"); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate: %v\n%s", err, out)
	}

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
