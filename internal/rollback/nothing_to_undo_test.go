// Tests that a present, empty rollback: list, rollback: [], plans as nothing
// to undo: no step and no refusal, reported in Plan.NothingToUndo, and told
// apart from no list at all, which falls back to the recorded undo or
// refuses. Before, an empty list read as absent, so a task whose author
// said there was nothing to undo was refused as one that could not be.
package rollback_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// authoredEntry is a changed entry whose journal says its task had a
// rollback: list.
func authoredEntry(node string, seq int, fqcn string) engine.JournalEntry {
	e := entry("run-A", node, "h1", seq, fqcn, engine.OutcomeChanged)
	e.AuthoredRollback = true
	return e
}

func TestBuild_AnEmptyRollbackListIsNothingToUndo(t *testing.T) {
	registerShapes(t)
	run := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{authoredEntry("tasks[0]", 1, "rb.shell")}}
	req := rollback.Request{Target: run, Devices: inventoryOf("h1"), Authored: func(string) ([]engine.Task, error) {
		return []engine.Task{}, nil
	}}
	plan, err := rollback.Build(req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Levels) != 0 || len(plan.AlreadyUndone) != 0 || len(plan.Left) != 0 {
		t.Errorf("levels %v, already undone %v, left %v; want none of them", plan.Levels, plan.AlreadyUndone, plan.Left)
	}
	if len(plan.NothingToUndo) != 1 || plan.NothingToUndo[0].Node != "tasks[0]" || plan.NothingToUndo[0].DeviceID != "h1" ||
		!strings.Contains(plan.NothingToUndo[0].Reason, "empty") {
		t.Errorf("nothing to undo %+v, want tasks[0] on h1, saying its list is empty", plan.NothingToUndo)
	}

	// The list taken out of the runbook since the run is not an empty one.
	req.Authored = func(string) ([]engine.Task, error) { return nil, nil }
	if problems := refused(t, func() error { _, err := rollback.Build(req); return err }()); len(problems) != 1 ||
		!strings.Contains(problems[0].Reason, "no longer gives it one") {
		t.Errorf("problems %+v, want the missing list refused", problems)
	}
}

// TestBuild_AnEmptyListWrittenAfterTheRunWinsOverARecordedUndo: the journal
// does not say the task had a list, as when it was written after the run;
// an empty one still answers, over the undo the method recorded, exactly as
// a list with steps would.
func TestBuild_AnEmptyListWrittenAfterTheRunWinsOverARecordedUndo(t *testing.T) {
	registerShapes(t)
	run := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		entry("run-A", "tasks[0]", "h1", 1, "rb.shell", engine.OutcomeChanged),
		undo(entry("run-A", "tasks[1]", "h1", 2, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "bsd", "uuid": "u-1"}),
	}}
	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1"), Authored: func(string) ([]engine.Task, error) {
		return []engine.Task{}, nil
	}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if steps := plan.Steps(); len(steps) != 0 || len(plan.NothingToUndo) != 2 {
		t.Errorf("steps %+v, nothing to undo %+v; want no step and both nodes nothing to undo", steps, plan.NothingToUndo)
	}
}

// TestBuild_NothingToUndoBesideAnUndoneChangeLeavesNothing: the run undid its
// one undoable change itself, and the other's list is empty, so a rollback
// of it is refused as having nothing left, not run as a rollback of nothing.
func TestBuild_NothingToUndoBesideAnUndoneChangeLeavesNothing(t *testing.T) {
	registerShapes(t)
	run := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		authoredEntry("tasks[0]", 1, "rb.shell"),
		undo(entry("run-A", "tasks[1]", "h1", 2, "rb.start", engine.OutcomeChanged), "rb.stop", map[string]any{"name": "svc"}),
		selfUndo("tasks[1]", 3, "rb.stop", engine.OutcomeChanged),
	}}
	authored := map[string][]engine.Task{"tasks[0]": {}}
	_, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1"), Authored: func(node string) ([]engine.Task, error) {
		return authored[node], nil
	}})
	if problems := refused(t, err); len(problems) != 1 || !strings.Contains(problems[0].Reason, "nothing left to undo") {
		t.Errorf("problems %+v, want one saying nothing is left", problems)
	}
}
