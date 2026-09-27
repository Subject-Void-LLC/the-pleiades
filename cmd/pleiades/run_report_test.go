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
	for name, c := range map[string]struct {
		mode   collection.Mode
		failed bool
		tasks  []taskReport
		status string
		exit   int
		msg    string
	}{
		"a run":                       {collection.ModeExecute, false, []taskReport{ok}, "complete", 0, "run complete"},
		"a failed run":                {collection.ModeExecute, true, []taskReport{ok}, "failed", 1, "execution failed"},
		"a failed check":              {collection.ModeCheck, true, []taskReport{ok}, "failed", 1, "check failed"},
		"a check":                     {collection.ModeCheck, false, []taskReport{ok}, "complete", 0, "check complete: nothing was changed"},
		"a check with a gap":          {collection.ModeCheck, false, []taskReport{unchecked, allowed}, "incomplete", exitIncomplete, "check incomplete: 1 task(s)"},
		"a check with a named gap":    {collection.ModeCheck, false, []taskReport{allowed, ok}, "complete", 0, "not checked, as --allow-unchecked allows: tasks[1]"},
		"a run with a check_mode gap": {collection.ModeExecute, false, []taskReport{unchecked}, "incomplete", exitIncomplete, "run incomplete: 1 task(s)"},
	} {
		got := outcomeOf(&runReport{Mode: c.mode, Tasks: c.tasks}, c.failed)
		if got.Status != c.status || got.ExitCode != c.exit || !strings.Contains(got.Message, c.msg) {
			t.Errorf("%s: %+v", name, got)
		}
		err := outcomeError(got)
		var incomplete *incompleteError
		switch {
		case c.status == "complete" && err != nil,
			c.status == "incomplete" && !errors.As(err, &incomplete),
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
		"  tasks[4]: FAILED: boom\n",
		"  tasks[5]: COULD NOT CHECK (no check) [allowed by --allow-unchecked]\n",
		"  tasks[6]: ok\n    provided by: ext (sha256:ab)\n",
		"metadata:\n  summary:\n    count: 3\n",
		"\nrun complete\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// A failed task's stats and an unchecked task's stats are not printed.
	if strings.Contains(out, "rc: 1") || strings.Contains(out, "hidden") {
		t.Errorf("printed stats it should not have:\n%s", out)
	}

	rep.Outcome = runOutcome{Status: "incomplete", Message: "check incomplete"}
	if out := captureStdout(t, func() { printResults(rep, false) }); strings.Contains(out, "rc: 0") || strings.Contains(out, "check incomplete") {
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
