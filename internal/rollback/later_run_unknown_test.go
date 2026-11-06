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
