// Tests that a refusal over a later run says which kind of run it is (a run
// with changes of its own to undo, one whose changes cannot be undone, one
// whose runbook says none needs undoing), and that a change whose undo needs
// a runbook that was not found is marked so --runbook can be named first.
package rollback_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

func TestBuild_TellsLaterRunsApart(t *testing.T) {
	registerShapes(t)
	empty := func(e engine.JournalEntry) engine.JournalEntry {
		e.AuthoredRollback, e.AuthoredRollbackEmpty = true, true
		return e
	}
	for name, tc := range map[string]struct {
		entries []engine.JournalEntry
		kind    string
		says    string
	}{
		"a run with an undo of its own": {
			entries: []engine.JournalEntry{
				undo(entry("run-B", "tasks[0]", "h1", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "bsd"}),
				entry("run-B", "tasks[1]", "h1", 21, "rb.shell", engine.OutcomeChanged),
			},
			kind: rollback.KindUndoFirst, says: "roll it back first (pleiades rollback run-B)",
		},
		"a run whose changes cannot be undone": {
			entries: []engine.JournalEntry{
				entry("run-B", "tasks[0]", "h1", 20, "rb.shell", engine.OutcomeChanged),
				empty(entry("run-B", "tasks[1]", "h1", 21, "rb.edit", engine.OutcomeChanged)),
			},
			kind: rollback.KindCannotUndo, says: "rb.shell recorded no undo",
		},
		"a run whose runbook says nothing needs undoing": {
			entries: []engine.JournalEntry{
				empty(entry("run-B", "tasks[0]", "h1", 20, "rb.shell", engine.OutcomeChanged)),
				empty(entry("run-B", "tasks[1]", "h1", 21, "rb.edit", engine.OutcomeChanged)),
			},
			kind: rollback.KindNothingToUndo, says: "none of its changes there needs undoing",
		},
	} {
		t.Run(name, func(t *testing.T) {
			later := rollback.Run{ID: "run-B", Sealed: true, Entries: tc.entries}
			_, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later}, Devices: inventoryOf("h1")})
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Kind != tc.kind || !strings.Contains(problems[0].Reason, tc.says) || problems[0].Accept != "--despite-run run-B" {
				t.Fatalf("problems %+v, want kind %s saying %q", problems, tc.kind, tc.says)
			}
		})
	}
}

// TestBuild_ARolledBackRunsNothingToUndoChangesCountAsUndone: once a later
// run is rolled back, a change of its whose runbook said it needs no undo
// does not keep it standing, since no step could ever have run for it.
func TestBuild_ARolledBackRunsNothingToUndoChangesCountAsUndone(t *testing.T) {
	registerShapes(t)
	nothing := entry("run-B", "tasks[1]", "h1", 21, "rb.shell", engine.OutcomeChanged)
	nothing.AuthoredRollback, nothing.AuthoredRollbackEmpty = true, true
	later := rollback.Run{ID: "run-B", Sealed: true, Entries: []engine.JournalEntry{
		undo(entry("run-B", "tasks[0]", "h1", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "bsd"}),
		nothing,
	}}
	undoB := rollback.Run{ID: "run-RB", Sealed: true, Entries: []engine.JournalEntry{func() engine.JournalEntry {
		e := entry("run-RB", "tasks[0]", "h1", 30, "rb.start", engine.OutcomeChanged)
		e.RollbackOf, e.UndoesNode = "run-B", "tasks[0]"
		return e
	}()}}
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later, undoB}, Devices: inventoryOf("h1")}); err != nil {
		t.Fatalf("after run-B was rolled back, Build = %v", err)
	}
}

// TestBuild_MarksAMissingRunbook: a change whose undo is its rollback: list,
// when the runbook is not found as it ran, is a no_runbook problem.
func TestBuild_MarksAMissingRunbook(t *testing.T) {
	registerShapes(t)
	e := entry("run-A", "tasks[0]", "h1", 1, "rb.shell", engine.OutcomeChanged)
	e.AuthoredRollback = true
	target := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{e}}
	missing := func(string) ([]engine.Task, error) {
		return nil, fmt.Errorf("%w: no runbook in runbooks is x as it ran", rollback.ErrNoRunbook)
	}
	for name, authored := range map[string]func(string) ([]engine.Task, error){"not found": missing, "no lookup": nil} {
		_, err := rollback.Build(rollback.Request{Target: target, Devices: inventoryOf("h1"), Authored: authored})
		if problems := refused(t, err); len(problems) != 1 || problems[0].Kind != rollback.KindNoRunbook {
			t.Errorf("%s: problems %+v, want one no_runbook", name, problems)
		}
	}
}

// TestProvisional_NamesEveryStepAnEarlierStepPrecedes: a step on a device an
// earlier level of the plan changes is provisional, named by the node id
// Runbook gives it; the first step on each device is not, and neither is a
// step beside it in the same level.
func TestProvisional_NamesEveryStepAnEarlierStepPrecedes(t *testing.T) {
	step := func(node, device string) rollback.Step {
		return rollback.Step{Node: node, DeviceID: device, DeviceName: "name-of-" + device, Name: "undo " + node}
	}
	levels := [][]rollback.Step{
		{step("tasks[3]", "h1")},
		{step("tasks[2]", "h1"), step("tasks[2]", "h2")},
		{step("tasks[1]", "h2")},
	}
	got := rollback.Provisional(levels)
	if len(got) != 2 || !strings.Contains(got["tasks[1].parallel[0]"], "undo tasks[3]") || !strings.Contains(got["tasks[2]"], "undo tasks[2]") || !strings.Contains(got["tasks[2]"], "name-of-h2") {
		t.Fatalf("Provisional = %v", got)
	}
	_, undoes, err := rollback.Runbook("r", levels, true)
	if err != nil {
		t.Fatal(err)
	}
	for id := range got {
		if _, ok := undoes[id]; !ok {
			t.Errorf("%s is not a node of the runbook (%v)", id, undoes)
		}
	}
}
