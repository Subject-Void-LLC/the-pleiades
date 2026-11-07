// Tests that a later run whose task failed during its action on a device
// this rollback undoes on refuses the rollback, as the same failure in the
// run being undone does: the journal cannot say what such a task changed.
package rollback_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// failedAt is a failed entry of a run, failed at stage.
func failedAt(run, node, device string, seq int, fqcn string, stage engine.FailureStage) engine.JournalEntry {
	e := entry(run, node, device, seq, fqcn, engine.OutcomeFailed)
	e.FailureStage = stage
	return e
}

// TestBuild_ALaterRunThatFailedDuringItsActionRefuses: each later run below
// touched h1, where run-A's changes are undone, after run-A began. One whose
// task failed during its action may have changed h1 and refuses, naming the
// method and the flag that steps over it; one that failed before reaching
// the device, failed in a read-only method, or failed on another device does
// not.
func TestBuild_ALaterRunThatFailedDuringItsActionRefuses(t *testing.T) {
	registerShapes(t)
	for name, tc := range map[string]struct {
		entries []engine.JournalEntry
		kind    string
		says    string
	}{
		"only a failure during its action": {
			entries: []engine.JournalEntry{failedAt("run-B", "tasks[0]", "h1", 20, "rb.shell", engine.FailureStageAction)},
			kind:    rollback.KindUnknownEffect, says: "rb.shell failed during its action",
		},
		"an undoable change and a failure during its action": {
			entries: []engine.JournalEntry{
				undo(entry("run-B", "tasks[0]", "h1", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "bsd"}),
				failedAt("run-B", "tasks[1]", "h1", 21, "rb.edit", engine.FailureStageAction),
			},
			kind: rollback.KindUndoFirst, says: "rb.edit failed during its action",
		},
		"a change with no undo and a failure during its action": {
			entries: []engine.JournalEntry{
				entry("run-B", "tasks[0]", "h1", 20, "rb.shell", engine.OutcomeChanged),
				failedAt("run-B", "tasks[1]", "h1", 21, "rb.edit", engine.FailureStageAction),
			},
			kind: rollback.KindCannotUndo, says: "rb.edit failed during its action",
		},
	} {
		t.Run(name, func(t *testing.T) {
			later := rollback.Run{ID: "run-B", Sealed: true, Entries: tc.entries}
			_, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later}, Devices: inventoryOf("h1")})
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Kind != tc.kind || !strings.Contains(problems[0].Reason, tc.says) || problems[0].Accept != "--despite-run run-B" {
				t.Fatalf("problems %+v, want kind %s saying %q", problems, tc.kind, tc.says)
			}
			// The method that failed is named as data too, whatever the kind.
			if failed := strings.Fields(tc.says)[0]; !slices.Equal(problems[0].UnknownEffects, []string{failed}) {
				t.Errorf("unknown effects %v, want [%s]", problems[0].UnknownEffects, failed)
			}
			req := rollback.Request{Target: createRun(), Others: []rollback.Run{later}, Devices: inventoryOf("h1"),
				DespiteRuns: map[string]bool{"run-B": true}}
			if _, err := rollback.Build(req); err != nil {
				t.Errorf("with --despite-run run-B, Build = %v", err)
			}
		})
	}

	for name, e := range map[string]engine.JournalEntry{
		"a failure before it reached the device": failedAt("run-B", "tasks[0]", "h1", 20, "rb.shell", engine.FailureStageLockDevice),
		"a read-only method's failure":           failedAt("run-B", "tasks[0]", "h1", 20, "rb.read", engine.FailureStageAction),
		"a failure on another device":            failedAt("run-B", "tasks[0]", "h2", 20, "rb.shell", engine.FailureStageAction),
	} {
		t.Run(name, func(t *testing.T) {
			later := rollback.Run{ID: "run-B", Sealed: true, Entries: []engine.JournalEntry{e}}
			if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{later}, Devices: inventoryOf("h1", "h2")}); err != nil {
				t.Errorf("Build = %v, want the rollback planned", err)
			}
		})
	}
}

// TestBuild_AFailureBeforeTheRunBeganDoesNotRefuse: a run that failed during
// its action on h1 before run-A began is under run-A's changes, not over
// them, so it is no reason to refuse.
func TestBuild_AFailureBeforeTheRunBeganDoesNotRefuse(t *testing.T) {
	registerShapes(t)
	earlier := rollback.Run{ID: "run-0", Sealed: true, Entries: []engine.JournalEntry{failedAt("run-0", "tasks[0]", "h1", -10, "rb.shell", engine.FailureStageAction)}}
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{earlier}, Devices: inventoryOf("h1")}); err != nil {
		t.Errorf("Build = %v, want the rollback planned", err)
	}
}

// rolledBack is a rollback of run-B that undid its tasks[0] on h1.
func rolledBack() rollback.Run {
	e := entry("run-RB", "tasks[0]", "h1", 30, "rb.start", engine.OutcomeChanged)
	e.RollbackOf, e.UndoesNode = "run-B", "tasks[0]"
	return rollback.Run{ID: "run-RB", Sealed: true, Entries: []engine.JournalEntry{e}}
}

// TestBuild_ARolledBackLaterRunStillRefusesForWhatFailedDuringItsAction:
// run-B changed h1 with an undo and then failed during its action on h1;
// run-RB rolled run-B back, accepting the failed task (--allow-unknown), so
// its one change is undone. What the failed task did is still on h1, which
// no rollback undoes, so undoing run-A beneath run-B is refused as an
// unknown effect, where it used to read run-B as cancelled out. With no
// failed task, the two runs cancel out and the rollback is planned.
func TestBuild_ARolledBackLaterRunStillRefusesForWhatFailedDuringItsAction(t *testing.T) {
	registerShapes(t)
	changed := undo(entry("run-B", "tasks[0]", "h1", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "bsd"})
	later := rollback.Run{ID: "run-B", Sealed: true, Entries: []engine.JournalEntry{
		changed, failedAt("run-B", "tasks[1]", "h1", 21, "rb.edit", engine.FailureStageAction),
	}}
	req := rollback.Request{Target: createRun(), Others: []rollback.Run{later, rolledBack()}, Devices: inventoryOf("h1")}
	_, err := rollback.Build(req)
	problems := refused(t, err)
	if len(problems) != 1 || problems[0].Kind != rollback.KindUnknownEffect || problems[0].Accept != "--despite-run run-B" ||
		!slices.Equal(problems[0].UnknownEffects, []string{"rb.edit"}) {
		t.Fatalf("problems %+v, want one unknown_effect over run-B naming rb.edit", problems)
	}
	// Its change was undone, so h1 is named only as where it may have
	// changed something.
	if reason := problems[0].Reason; !strings.HasPrefix(reason, "run run-B may have changed name-of-h1 after") || strings.Contains(reason, "roll it back first") {
		t.Errorf("reason %q, want h1 named only as maybe changed, with no advice to roll back again", reason)
	}
	req.DespiteRuns = map[string]bool{"run-B": true}
	if _, err := rollback.Build(req); err != nil {
		t.Errorf("with --despite-run run-B, Build = %v", err)
	}

	clean := rollback.Run{ID: "run-B", Sealed: true, Entries: []engine.JournalEntry{changed}}
	if _, err := rollback.Build(rollback.Request{Target: createRun(), Others: []rollback.Run{clean, rolledBack()}, Devices: inventoryOf("h1")}); err != nil {
		t.Errorf("with no failed task, run-B and its rollback cancel out, but Build = %v", err)
	}
}

// TestBuild_ALaterRunsAdviceSaysWhatClearsAFailedTask: when a later run
// changed one device and failed during its action on another, the refusal
// names the first as changed and the second as maybe changed, and its
// advice clears the failure: rolling the run back leaves what failed, so
// it says to step over it with --despite-run, for an undoable run and for
// one whose changes cannot be undone alike. A device it both changed and
// failed on is named once, as changed.
func TestBuild_ALaterRunsAdviceSaysWhatClearsAFailedTask(t *testing.T) {
	registerShapes(t)
	target := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		undo(entry("run-A", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "a", "uuid": "u-1"}),
		undo(entry("run-A", "tasks[0]", "h2", 2, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "b", "uuid": "u-2"}),
	}}
	failedOnH2 := failedAt("run-B", "tasks[1]", "h2", 21, "rb.edit", engine.FailureStageAction)
	for name, tc := range map[string]struct {
		entries []engine.JournalEntry
		kind    string
		says    []string
		never   []string
	}{
		"an undoable change": {
			entries: []engine.JournalEntry{undo(entry("run-B", "tasks[0]", "h1", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "a"}), failedOnH2},
			kind:    rollback.KindUndoFirst,
			says: []string{"run run-B changed name-of-h1 and may have changed name-of-h2 after this run began",
				"pleiades rollback run-B, accepting the failed task with --allow-unknown", "step over it with --despite-run run-B"},
			never: []string{"), or step over it"},
		},
		"a change with no undo": {
			entries: []engine.JournalEntry{entry("run-B", "tasks[0]", "h1", 20, "rb.shell", engine.OutcomeChanged), failedOnH2},
			kind:    rollback.KindCannotUndo,
			says:    []string{"run run-B changed name-of-h1 and may have changed name-of-h2 after this run began", "rb.shell recorded no undo", "step over it with --despite-run run-B"},
		},
		"a change and a failure on one device": {
			entries: []engine.JournalEntry{
				undo(entry("run-B", "tasks[0]", "h1", 20, "rb.stop", engine.OutcomeChanged), "rb.start", map[string]any{"name": "a"}),
				failedAt("run-B", "tasks[1]", "h1", 21, "rb.edit", engine.FailureStageAction),
			},
			kind:  rollback.KindUndoFirst,
			says:  []string{"run run-B changed name-of-h1 after this run began", "step over it with --despite-run run-B"},
			never: []string{"may have changed name-of-h1"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			later := rollback.Run{ID: "run-B", Sealed: true, Entries: tc.entries}
			_, err := rollback.Build(rollback.Request{Target: target, Others: []rollback.Run{later}, Devices: inventoryOf("h1", "h2")})
			problems := refused(t, err)
			if len(problems) != 1 || problems[0].Kind != tc.kind {
				t.Fatalf("problems %+v, want one of kind %s", problems, tc.kind)
			}
			for _, s := range tc.says {
				if !strings.Contains(problems[0].Reason, s) {
					t.Errorf("reason %q does not say %q", problems[0].Reason, s)
				}
			}
			for _, s := range tc.never {
				if strings.Contains(problems[0].Reason, s) {
					t.Errorf("reason %q says %q", problems[0].Reason, s)
				}
			}
		})
	}
}
