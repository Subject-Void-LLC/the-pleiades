// Tests that a change a block's always: task made is left in place by
// default, reported once in Plan.Cleanups with why, and undone only when its
// runbook gives the task a rollback: list. Before, a teardown in always:
// (virt.cml.lab.delete) was a change with no undo, so every rollback of its
// run needed --leave for it.
package rollback_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// cleanupRun is a run whose block made a VM (undone by a recorded delete),
// and whose always: tore something down with no undo (rb.shell), and stopped
// a service with a recorded undo that would start it again (rb.stop).
func cleanupRun() rollback.Run {
	return rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		undo(entry("run-A", "tasks[0].block[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "bsd", "uuid": "u-1"}),
		entry("run-A", "tasks[0].always[0]", "h1", 2, "rb.shell", engine.OutcomeChanged),
		undo(entry("run-A", "tasks[0].always[1]", "h1", 3, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "svc"}),
	}}
}

// noLists is a runbook lookup that finds the runbook and no rollback: list
// on any task.
func noLists(string) ([]engine.Task, error) { return nil, nil }

// TestBuild_AnAlwaysCleanupIsLeftInPlaceAndSaidOnce: the block's change is
// undone; both always: changes, the one with no undo and the one whose
// recorded undo would reverse the cleanup, are left in place without a
// --leave and reported once each with the reason.
func TestBuild_AnAlwaysCleanupIsLeftInPlaceAndSaidOnce(t *testing.T) {
	registerShapes(t)
	for _, authored := range []func(string) ([]engine.Task, error){nil, noLists,
		func(string) ([]engine.Task, error) { return nil, fmt.Errorf("lost: %w", rollback.ErrNoRunbook) }} {
		plan, err := rollback.Build(rollback.Request{Target: cleanupRun(), Devices: inventoryOf("h1"), Authored: authored})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		steps := plan.Steps()
		if len(steps) != 1 || steps[0].Node != "tasks[0].block[0]" || steps[0].FQCN != "rb.delete" {
			t.Errorf("steps %+v, want only the block's make undone", steps)
		}
		if len(plan.Cleanups) != 2 || plan.Cleanups[0].Node != "tasks[0].always[1]" || plan.Cleanups[1].Node != "tasks[0].always[0]" ||
			plan.Cleanups[0].DeviceName != "name-of-h1" || !strings.Contains(plan.Cleanups[0].Reason, "always:") || !strings.Contains(plan.Cleanups[0].Reason, "rollback: list") {
			t.Errorf("cleanups %+v, want both always: changes, newest first, saying why and what undoes one", plan.Cleanups)
		}
		if len(plan.Left) != 0 || len(plan.NothingToUndo) != 0 {
			t.Errorf("left %+v, nothing to undo %+v; want neither", plan.Left, plan.NothingToUndo)
		}
	}
}

// TestBuild_AnAlwaysTaskWithARollbackListIsUndone: a rollback: list on the
// always: task, recorded with the run or written since, is the author's
// word that its change should be undone, so it is planned like any task's,
// and an empty one is nothing to undo.
func TestBuild_AnAlwaysTaskWithARollbackListIsUndone(t *testing.T) {
	registerShapes(t)
	list := []engine.Task{{Name: "bring it back", FQCN: "rb.start", Params: map[string]any{"name": "lab"}}}
	for _, recorded := range []bool{true, false} {
		run := cleanupRun()
		run.Entries[1].AuthoredRollback = recorded
		plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1"), Authored: func(node string) ([]engine.Task, error) {
			if node == "tasks[0].always[0]" {
				return list, nil
			}
			return nil, nil
		}})
		if err != nil {
			t.Fatalf("recorded %v: Build: %v", recorded, err)
		}
		steps := plan.Steps()
		if len(steps) != 2 || steps[0].Node != "tasks[0].always[0]" || steps[0].FQCN != "rb.start" || steps[0].Source != "authored" {
			t.Errorf("recorded %v: steps %+v, want the always: task's own list first", recorded, steps)
		}
		if len(plan.Cleanups) != 1 || plan.Cleanups[0].Node != "tasks[0].always[1]" {
			t.Errorf("recorded %v: cleanups %+v, want only the task with no list", recorded, plan.Cleanups)
		}
	}
	plan, err := rollback.Build(rollback.Request{Target: cleanupRun(), Devices: inventoryOf("h1"), Authored: func(node string) ([]engine.Task, error) {
		if node == "tasks[0].always[0]" {
			return []engine.Task{}, nil
		}
		return nil, nil
	}})
	if err != nil || len(plan.NothingToUndo) != 1 || plan.NothingToUndo[0].Node != "tasks[0].always[0]" {
		t.Errorf("an empty list on an always: task: %v, nothing to undo %+v", err, plan.NothingToUndo)
	}
}

// TestBuild_AnAlwaysCleanupEdges: a cleanup inside a nested block's always:
// is one too, and a plain block task is not; a lookup that fails for another
// reason than a missing runbook is reported, not taken for no list; --leave
// still names one; --allow-skew for one accepts nothing where the planner
// runs the plan; and an always: task that failed during its action is still
// an unknown effect, since a cleanup that stopped partway is not one done.
func TestBuild_AnAlwaysCleanupEdges(t *testing.T) {
	registerShapes(t)
	nested := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		entry("run-A", "tasks[0].block[1].always[0].block[0]", "h1", 1, "rb.shell", engine.OutcomeChanged),
	}}
	if plan, err := rollback.Build(rollback.Request{Target: nested, Devices: inventoryOf("h1"), Authored: noLists}); err != nil || len(plan.Cleanups) != 1 {
		t.Errorf("a nested always: %v, cleanups %+v", err, plan.Cleanups)
	}
	plain := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		entry("run-A", "tasks[0].block[0]", "h1", 1, "rb.shell", engine.OutcomeChanged),
	}}
	if problems := refused(t, func() error {
		_, err := rollback.Build(rollback.Request{Target: plain, Devices: inventoryOf("h1"), Authored: noLists})
		return err
	}()); len(problems) != 1 || problems[0].Accept != "--leave tasks[0].block[0]" {
		t.Errorf("a block task with no undo: %+v, want it refused as before", problems)
	}

	_, err := rollback.Build(rollback.Request{Target: cleanupRun(), Devices: inventoryOf("h1"), Authored: func(string) ([]engine.Task, error) {
		return nil, errors.New("runbook does not parse")
	}})
	if problems := refused(t, err); len(problems) != 3 || !strings.Contains(problems[0].Reason, "runbook does not parse") || !strings.Contains(problems[1].Node, "always") {
		t.Errorf("a failed lookup: %+v, want every node, the always: ones too, refused with it", problems)
	}

	plan, err := rollback.Build(rollback.Request{Target: cleanupRun(), Devices: inventoryOf("h1"), Authored: noLists, Leave: map[string]bool{"tasks[0].always[0]": true}})
	if err != nil || len(plan.Left) != 1 || plan.Left[0].Node != "tasks[0].always[0]" || len(plan.Cleanups) != 1 {
		t.Errorf("--leave on a cleanup: %v, left %+v, cleanups %+v", err, plan.Left, plan.Cleanups)
	}

	skew := map[string]bool{"tasks[0].always[1]": true}
	_, err = rollback.Build(rollback.Request{Target: cleanupRun(), Devices: inventoryOf("h1"), Authored: noLists, AllowSkew: skew, ExecutorIsPlanner: true})
	if problems := refused(t, err); len(problems) != 1 || !strings.Contains(problems[0].Reason, "--allow-skew accepts nothing") {
		t.Errorf("--allow-skew on a cleanup: %+v", problems)
	}
	if _, err := rollback.Build(rollback.Request{Target: cleanupRun(), Devices: inventoryOf("h1"), Authored: noLists, AllowSkew: skew}); err != nil {
		t.Errorf("--allow-skew on a cleanup where a Runner runs the plan: %v", err)
	}

	failed := cleanupRun()
	failed.Entries[1].Outcome, failed.Entries[1].ActionChanged, failed.Entries[1].FailureStage = engine.OutcomeFailed, false, engine.FailureStageAction
	plan, err = rollback.Build(rollback.Request{Target: failed, Devices: inventoryOf("h1"), Authored: noLists})
	if err != nil || len(plan.Unknown) != 1 || plan.Unknown[0].Node != "tasks[0].always[0]" || plan.UnacceptedUnknown != 1 {
		t.Errorf("a failed cleanup: %v, unknown %+v", err, plan.Unknown)
	}
}

// TestBuild_AnUndoneRunWithACleanupHasNothingLeft: once a rollback undid the
// block's change, a second rollback of the run is refused as having nothing
// left, the cleanups counted as left, not run as a rollback of nothing.
func TestBuild_AnUndoneRunWithACleanupHasNothingLeft(t *testing.T) {
	registerShapes(t)
	undone := entry("run-R", "tasks[0]", "h1", 10, "rb.delete", engine.OutcomeChanged)
	undone.RollbackOf, undone.UndoesNode = "run-A", "tasks[0].block[0]"
	rolled := rollback.Run{ID: "run-R", Sealed: true, Entries: []engine.JournalEntry{undone}}
	_, err := rollback.Build(rollback.Request{Target: cleanupRun(), Others: []rollback.Run{rolled}, Devices: inventoryOf("h1"), Authored: noLists})
	if problems := refused(t, err); len(problems) != 1 || !strings.Contains(problems[0].Reason, "already been undone") {
		t.Errorf("problems %+v, want one saying nothing is left", problems)
	}
}
