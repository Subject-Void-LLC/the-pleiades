// Tests that the planner reads a run's own undos, the steps a block's
// rollback_on_failure ran inside the run, as already done: a later rollback
// of the run does not repeat them, and the run is not mistaken for a
// rollback of itself.
package rollback_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// selfUndo is a step run-A ran itself, undoing node on h1.
func selfUndo(node string, seq int, fqcn string, outcome engine.Outcome) engine.JournalEntry {
	e := entry("run-A", node+".undo[0]", "h1", seq, fqcn, outcome)
	e.RollbackOf, e.UndoesNode = "run-A", node
	if outcome == engine.OutcomeFailed {
		e.FailureStage = engine.FailureStageAction
	}
	return e
}

// TestBuild_AStepTheRunRanItselfIsAlreadyUndone: run-A started a VM and its
// block's rollback_on_failure stopped it again; a rollback of run-A plans
// only the creation's undo, names the start as undone by the run itself,
// and does not refuse run-A as a rollback of something.
func TestBuild_AStepTheRunRanItselfIsAlreadyUndone(t *testing.T) {
	registerShapes(t)
	run := createRun()
	run.Entries = append(run.Entries, selfUndo("tasks[1]", 4, "rb.stop", engine.OutcomeChanged))
	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if steps := plan.Steps(); len(steps) != 1 || steps[0].Node != "tasks[0]" || steps[0].FQCN != "rb.delete" {
		t.Errorf("steps %+v, want only tasks[0]'s delete", steps)
	}
	if len(plan.AlreadyUndone) != 1 || plan.AlreadyUndone[0].Node != "tasks[1]" || !strings.Contains(plan.AlreadyUndone[0].Reason, "the run undid it itself") {
		t.Errorf("already undone %+v, want tasks[1] undone by the run itself", plan.AlreadyUndone)
	}
}

// TestBuild_ARunThatUndidEverythingItselfHasNothingLeft: every change undone
// inside the run is a refusal saying so, not an empty rollback.
func TestBuild_ARunThatUndidEverythingItselfHasNothingLeft(t *testing.T) {
	registerShapes(t)
	run := createRun()
	run.Entries = append(run.Entries,
		selfUndo("tasks[1]", 4, "rb.stop", engine.OutcomeChanged),
		selfUndo("tasks[0]", 5, "rb.delete", engine.OutcomeChanged))
	_, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
	if problems := refused(t, err); len(problems) != 1 || !strings.Contains(problems[0].Reason, "by the run itself") {
		t.Errorf("problems %+v, want one saying the run undid everything itself", problems)
	}
}

// TestBuild_AnUndoThatFailedInTheRunIsPlannedAgain: the run's own undo of
// the start failed, so the start is still to undo; the failed step is not
// an unknown change either, since it is an undo, not part of the run's work.
func TestBuild_AnUndoThatFailedInTheRunIsPlannedAgain(t *testing.T) {
	registerShapes(t)
	run := createRun()
	run.Entries = append(run.Entries, selfUndo("tasks[1]", 4, "rb.stop", engine.OutcomeFailed))
	plan, err := rollback.Build(rollback.Request{Target: run, Devices: inventoryOf("h1")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if steps := plan.Steps(); len(steps) != 2 || len(plan.AlreadyUndone) != 0 {
		t.Errorf("steps %+v, already undone %+v; want both changes planned", steps, plan.AlreadyUndone)
	}
	if len(plan.Unknown) != 0 {
		t.Errorf("the failed undo read as an unknown change: %+v", plan.Unknown)
	}
}
