//go:build unix

// Tests for a pleiades run that printed no report, through a stand-in
// binary run the way a check runs the real one.
package oscheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standIn writes a shell script as a pleiades binary in a new directory and
// returns a Pleiades that runs it.
func standIn(t *testing.T, body string) Pleiades {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pleiades")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { // #nosec G306 -- a test's own script, run by the test
		t.Fatal(err)
	}
	return Pleiades{Binary: path, Project: t.TempDir(), Device: "vengeance"}
}

// TestAdhoc_ARunWithNoReportSaysHowItEnded: a pleiades that writes only to
// stderr and exits 3 is reported with its exit status and the end of its
// stderr, by every caller, rather than as a JSON parse error; one killed by
// a signal names the signal.
func TestAdhoc_ARunWithNoReportSaysHowItEnded(t *testing.T) {
	ctx := context.Background()
	p := standIn(t, `for i in 1 2 3 4 5 6 7; do echo "line $i" >&2; done; exit 3`)
	_, statsErr := p.AdhocStats(ctx, "virt.vbox.vm.stop", "name=bsd-check")
	_, stdoutErr := p.Adhoc(ctx, "virt.vbox.vm.stop")
	_, outcomeErr := p.AdhocOutcome(ctx, "exec.command")
	for name, err := range map[string]error{"AdhocStats": statsErr, "Adhoc": stdoutErr, "AdhocOutcome": outcomeErr} {
		if err == nil || !strings.Contains(err.Error(), "printed no report and ended with exit status 3: line 3") ||
			!strings.Contains(err.Error(), "line 7") || strings.Contains(err.Error(), "line 2") {
			t.Errorf("%s: %v", name, err)
		}
	}
	killed := standIn(t, `kill -9 $$`)
	if _, err := killed.AdhocStats(ctx, "virt.vbox.vm.stop"); err == nil || !strings.Contains(err.Error(), "signal: killed") {
		t.Errorf("a killed run: %v", err)
	}
	// A run that printed a report is read as one, whatever it exited with.
	reported := standIn(t, `echo '{"tasks":[{"status":"changed","stats":{"state":"poweroff"}}],"outcome":{"exit_code":0}}'; exit 1`)
	if stats, err := reported.AdhocStats(ctx, "virt.vbox.vm.stop"); err != nil || stats["state"] != "poweroff" {
		t.Errorf("a run with a report: %v, %v", stats, err)
	}
	// A clean exit with nothing printed is the report reader's to refuse.
	silent := standIn(t, `exit 0`)
	if _, err := silent.AdhocStats(ctx, "virt.vbox.vm.stop"); err == nil || !strings.Contains(err.Error(), "printed no report") {
		t.Errorf("a silent clean run: %v", err)
	}
}
