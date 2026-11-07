// The release gate for handlers: a runbook run twice through the real binary
// against a real Debian device, its handlers appending to a log on the device
// so how often and in what order they ran is read back from the device itself.
package main_test

import (
	"encoding/json"
	"testing"
)

// handlersGateRunbook has three tasks notifying restart (one changes only on
// its first run, one is never run) and one notifying reload, which is defined
// before restart though notified after it.
const handlersGateRunbook = `# Writes a config file and runs two handlers that log to /tmp/hg.log.
# Changes: /tmp/hg.conf and /tmp/hg.log.
# Undo: none recorded; delete both files by hand.
id: handlers-gate
hosts: debian
tasks:
  - name: write the config
    file.copy:
      dest: /tmp/hg.conf
      content: "v1\n"
    notify: restart
  - name: touch the unit
    exec.shell:
      cmd: "true"
    notify: config-changed
  - name: never runs
    exec.shell:
      cmd: "true"
    notify: restart
    when_cel: 1 == 2
handlers:
  - name: reload
    exec.shell:
      cmd: echo reload >> /tmp/hg.log
    listen: config-changed
  - name: restart
    exec.shell:
      cmd: echo restart >> /tmp/hg.log
`

// TestCLI_HandlersRunOnceInTheirOrderOnlyWhenNotified: the first run logs
// reload then restart, once each, in definition order though restart was
// notified first, the skipped task notifying nothing; the second run, where
// the config is already written and so changes nothing, logs only reload.
func TestCLI_HandlersRunOnceInTheirOrderOnlyWhenNotified(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the handlers release gate, which builds and runs a real Debian sshd, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")
	writeFile(t, dir, "runbooks/handlers.yaml", handlersGateRunbook)

	for i, want := range []string{"reload\nrestart", "reload\nrestart\nreload"} {
		out, err := runPleiadesWithHome(t, dir, home, "run", "runbooks/handlers.yaml")
		if err != nil {
			t.Fatalf("run %d: %v\n%s", i+1, err, out)
		}
		if got, code := device.exec(t, "cat", "/tmp/hg.log"); code != 0 || got != want {
			t.Fatalf("after run %d the handlers' log is %q (exit %d), want %q:\n%s", i+1, got, code, want, out)
		}
	}

	// Each run's journal reads back: the handlers' entries (node
	// tasks.handlers[N], and the second run's restart skipped as
	// not_notified) are ones the strict reader accepts, so journal and
	// rollback can read a run that had handlers.
	out, err := runPleiadesWithHome(t, dir, home, "journal", "list", "--json")
	if err != nil {
		t.Fatalf("journal list: %v\n%s", err, out)
	}
	var runs []struct {
		RunID string `json:"run_id"`
		Tasks int    `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) != 2 {
		t.Fatalf("journal list --json gave %d runs (%v), want 2:\n%s", len(runs), err, out)
	}
	for _, r := range runs {
		if out, err := runPleiadesWithHome(t, dir, home, "journal", "show", r.RunID, "--json"); err != nil {
			t.Errorf("journal show %s: %v\n%s", r.RunID, err, out)
		}
	}
}
