// Tests for the run report: how a run's outcome is decided, and how the
// text view prints each status from the same model --json writes.
package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestOutcomeOf(t *testing.T) {
	unchecked := taskReport{ID: "tasks[0]", Device: "d1", Status: statusUnchecked}
	allowed := taskReport{ID: "tasks[1]", Status: statusUnchecked, AllowedUnchecked: true}
	ok := taskReport{ID: "tasks[2]", Status: statusOK}
	rescued := taskReport{ID: "tasks[3].block[0]", Device: "d1", Status: statusFailed, RescuedBy: "tasks[3].rescue[0]"}
	for name, c := range map[string]struct {
		mode                  collection.Mode
		failed                bool
		rescued, failOnRescue bool
		tasks                 []taskReport
		status                string
		exit                  int
		msg                   string
	}{
		"a run":                               {collection.ModeExecute, false, false, false, []taskReport{ok}, "complete", 0, "run complete"},
		"a failed run":                        {collection.ModeExecute, true, false, false, []taskReport{ok}, "failed", 1, "execution failed"},
		"a failed check":                      {collection.ModeCheck, true, false, false, []taskReport{ok}, "failed", 1, "check failed"},
		"a check":                             {collection.ModeCheck, false, false, false, []taskReport{ok}, "complete", 0, "check complete: nothing was changed"},
		"a check with a gap":                  {collection.ModeCheck, false, false, false, []taskReport{unchecked, allowed}, "incomplete", exitIncomplete, "check incomplete: 1 task(s)"},
		"a check with a named gap":            {collection.ModeCheck, false, false, false, []taskReport{allowed, ok}, "complete", 0, "not checked, as --allow-unchecked allows: tasks[1]"},
		"a run with a check_mode gap":         {collection.ModeExecute, false, false, false, []taskReport{unchecked}, "incomplete", exitIncomplete, "run incomplete: 1 task(s)"},
		"a rescued run":                       {collection.ModeExecute, false, true, false, []taskReport{rescued, ok}, "rescued", 0, "answered the failure of tasks[3].block[0]"},
		"a rescued run, refused":              {collection.ModeExecute, false, true, true, []taskReport{rescued, ok}, "rescued", exitRescued, "--fail-on-rescue does not accept"},
		"a failed run that also had a rescue": {collection.ModeExecute, true, true, true, []taskReport{rescued, ok}, "failed", 1, "execution failed"},
	} {
		got := outcomeOf(&runReport{Mode: c.mode, Tasks: c.tasks}, c.failed, c.rescued, c.failOnRescue)
		if got.Status != c.status || got.ExitCode != c.exit || !strings.Contains(got.Message, c.msg) {
			t.Errorf("%s: %+v", name, got)
		}
		err := outcomeError(got)
		var incomplete *incompleteError
		var refused *rescuedError
		switch {
		case c.status == "complete" && err != nil,
			c.status == "incomplete" && !errors.As(err, &incomplete),
			c.status == "rescued" && c.exit == 0 && err != nil,
			c.status == "rescued" && c.exit == exitRescued && (!errors.As(err, &refused) || refused.ExitCode() != exitRescued),
			c.status == "failed" && (err == nil || err.Error() != got.Message):
			t.Errorf("%s: outcomeError = %v", name, err)
		}
	}
}

func TestPrintResultsPrintsEveryStatus(t *testing.T) {
	rep := &runReport{
		Mode: collection.ModeExecute,
		Tasks: []taskReport{
			{ID: "tasks[0]", Device: "d1", Status: statusChanged, Warnings: []string{"look\x1b[2J"}, Stats: map[string]any{"rc": 0}},
			{ID: "tasks[1]", Status: statusCheckedOnly},
			{ID: "tasks[2]", Status: statusWouldChange},
			{ID: "tasks[3]", Status: statusSkipped, Reason: "when was false"},
			{ID: "tasks[4]", Status: statusFailed, Error: "boom", Stats: map[string]any{"rc": 1}},
			{ID: "tasks[5]", Status: statusUnchecked, Reason: "no check", AllowedUnchecked: true, Stats: map[string]any{"hidden": true}},
			{ID: "tasks[6]", Status: statusOK, Provider: &collection.Provider{Program: "ext", Digest: "sha256:ab"}},
			{ID: "tasks[7]", Status: statusHeld, Error: "held", Stats: map[string]any{"withheld": true}},
			{ID: "tasks[8].block[0]", Device: "d1", Status: statusFailed, Error: "down", RescuedBy: "tasks[8].rescue[0]"},
			{ID: "tasks[8].rescue[0]", Status: statusConditionalSkip, Reason: "runs only if its block fails"},
		},
		Metadata: map[string]any{"summary": map[string]any{"": map[string]any{"count": 3}}},
		Outcome:  runOutcome{Status: "complete", Message: "run complete"},
	}
	out := captureStdout(t, func() { printResults(rep, true) })
	for _, want := range []string{
		"  tasks[0] [d1]: changed\n    WARNING: look\\x1b[2J\n    rc: 0\n",
		"  tasks[1]: ok (checked only)\n",
		"  tasks[2]: would change\n",
		"  tasks[3]: skipped (when was false)\n",
		// A failed task's output is printed, since it is usually why it failed.
		"  tasks[4]: FAILED: boom\n    rc: 1\n",
		"  tasks[5]: COULD NOT CHECK (no check) [allowed by --allow-unchecked]\n",
		"  tasks[6]: ok\n    provided by: ext (sha256:ab)\n",
		"  tasks[7]: HELD FOR REVIEW: held\n",
		"  tasks[8].block[0] [d1]: FAILED, RESCUED by tasks[8].rescue[0]: down\n",
		"  tasks[8].rescue[0]: not predicted (runs only if its block fails)\n",
		"metadata:\n  summary:\n    count: 3\n",
		"\nrun complete\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// An unchecked task's stats and a held task's are not printed.
	if strings.Contains(out, "hidden") || strings.Contains(out, "withheld") {
		t.Errorf("printed stats it should not have:\n%s", out)
	}

	rep.Outcome = runOutcome{Status: "incomplete", Message: "check incomplete"}
	if out := captureStdout(t, func() { printResults(rep, false) }); strings.Contains(out, "rc: ") || strings.Contains(out, "check incomplete") {
		t.Errorf("without --verbose, or for an incomplete run:\n%s", out)
	}
}

func TestPrintPlanPrintsTheHeader(t *testing.T) {
	rep := &runReport{
		Runbook: "site.yaml", Mode: collection.ModeCheck, Nodes: 2, InventoryHosts: 3,
		BlastRadius: blastRadius{Devices: 2, Tiers: []string{"lab"}}, Selection: "--tags web",
		Plan: []planSection{{Section: "tasks", Tasks: []planTask{{ID: "tasks[0]", Method: "noop", Selected: true}}}},
	}
	out := captureStdout(t, func() { printPlan(rep) })
	want := "plan for site.yaml (2 nodes, 3 inventory hosts loaded):\n" +
		"mode: check (tasks report what they would change; nothing on any device is changed)\n" +
		"service-effecting: false\nblast radius: 2 devices, tiers: [lab]\n\n" +
		"selection: --tags web\ntasks:\n  noop\n\nchecking:\n"
	if out != want {
		t.Errorf("printed:\n%q\nwant:\n%q", out, want)
	}
}

// TestPrintResultsEndsARescuedRunWithItsOwnLine: a rescued run that ends 0
// says so as its last line; one --fail-on-rescue refuses leaves its line to
// the error the command ends with.
func TestPrintResultsEndsARescuedRunWithItsOwnLine(t *testing.T) {
	rep := &runReport{Mode: collection.ModeExecute, Outcome: runOutcome{Status: "rescued", Message: "run complete through a rescue: x"}}
	if out := captureStdout(t, func() { printResults(rep, false) }); !strings.HasSuffix(out, "\nrun complete through a rescue: x\n") {
		t.Errorf("a rescued run ends %q", out)
	}
	rep.Outcome = runOutcome{Status: "rescued", ExitCode: exitRescued, Message: "run rescued: refused"}
	if out := captureStdout(t, func() { printResults(rep, false) }); strings.Contains(out, "run rescued") {
		t.Errorf("a refused rescued run printed its line: %q", out)
	}
}

// TestPrintResultsSaysADevicesWarningOnce: a warning every task on a device
// carries (a pinned certificate) is printed for its first task only, while
// another device's, or another warning, is printed again.
func TestPrintResultsSaysADevicesWarningOnce(t *testing.T) {
	pinned := "trusting the certificate pinned by tls_fingerprint"
	rep := &runReport{Mode: collection.ModeExecute, Outcome: runOutcome{Status: "complete", Message: "run complete"}, Tasks: []taskReport{
		{ID: "tasks[0]", Device: "cml-id", Status: statusChanged, Warnings: []string{pinned}},
		{ID: "tasks[1]", Device: "cml-id", Status: statusChanged, Warnings: []string{pinned, "a legacy cipher suite"}},
		{ID: "tasks[2]", Device: "other-id", Status: statusChanged, Warnings: []string{pinned}},
	}}
	out := captureStdout(t, func() { printResults(rep, false) })
	if n := strings.Count(out, "WARNING: "+pinned); n != 2 {
		t.Errorf("the pinned warning printed %d times, want once per device (2):\n%s", n, out)
	}
	if !strings.Contains(out, "WARNING: a legacy cipher suite") {
		t.Errorf("a second warning was dropped:\n%s", out)
	}
}
