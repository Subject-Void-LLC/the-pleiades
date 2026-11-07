// Golden test for the rollback planner: one fixed request covering each
// shape a plan reports, planned twice (with the operator's acceptances and
// without), and the plan and the refusal pinned as JSON. A plan has no
// published schema, yet `pleiades rollback --json` and the Walk tier's
// rollback job both carry it, so a silent change to its shape or its
// reasons fails here until testdata/golden/plan.json is updated on purpose.
package rollback_test

import (
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/golden"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// goldenRequest builds the fixed request: run-A's journal on h1 and h2, an
// earlier rollback that undid part of it, and a later run on h2.
func goldenRequest() rollback.Request {
	with := func(e engine.JournalEntry, edit func(*engine.JournalEntry)) engine.JournalEntry {
		edit(&e)
		return e
	}
	target := rollback.Run{ID: "run-A", Sealed: true, Entries: []engine.JournalEntry{
		// An exact undo on two devices in one level, run side by side.
		undo(entry("run-A", "tasks[0]", "h1", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "vm", "uuid": "u-1"}),
		undo(entry("run-A", "tasks[0]", "h2", 1, "rb.make", engine.OutcomeChanged), "rb.delete", map[string]any{"name": "vm", "uuid": "u-2"}),
		// An exact undo an earlier rollback already ran.
		undo(entry("run-A", "tasks[1]", "h1", 2, "rb.start", engine.OutcomeChanged), "rb.stop", map[string]any{"name": "vm"}),
		// A change with no undo, refused unless left.
		entry("run-A", "tasks[2]", "h1", 3, "rb.shell", engine.OutcomeChanged),
		// A change whose runbook authored its undo.
		with(entry("run-A", "tasks[3]", "h1", 4, "rb.shell", engine.OutcomeChanged), func(e *engine.JournalEntry) { e.AuthoredRollback = true }),
		// A change whose runbook says undoing it is nothing: rollback: [].
		with(entry("run-A", "tasks[4]", "h1", 5, "rb.shell", engine.OutcomeChanged), func(e *engine.JournalEntry) {
			e.AuthoredRollback, e.AuthoredRollbackEmpty = true, true
		}),
		// An action that failed partway: an unknown effect.
		with(entry("run-A", "tasks[5]", "h1", 6, "rb.shell", engine.OutcomeFailed), func(e *engine.JournalEntry) {
			e.FailureStage = engine.FailureStageAction
		}),
		// A partial undo, refused unless allowed.
		with(undo(entry("run-A", "tasks[6]", "h2", 7, "rb.copy", engine.OutcomeChanged), "rb.edit", map[string]any{"name": "f"}), func(e *engine.JournalEntry) {
			e.InversePartial, e.InverseComplete = true, false
		}),
		// A read, which is no change at all.
		entry("run-A", "tasks[7]", "h1", 8, "rb.read", engine.OutcomeRan),
	}}
	earlier := rollback.Run{ID: "run-R", Sealed: true, Entries: []engine.JournalEntry{
		with(entry("run-R", "tasks[0]", "h1", 10, "rb.stop", engine.OutcomeChanged), func(e *engine.JournalEntry) {
			e.RollbackOf, e.UndoesNode = "run-A", "tasks[1]"
		}),
	}}
	later := rollback.Run{ID: "run-B", Sealed: true, Entries: []engine.JournalEntry{
		undo(entry("run-B", "tasks[0]", "h2", 20, "rb.start", engine.OutcomeChanged), "rb.stop", map[string]any{"name": "svc"}),
	}}
	authored := map[string][]engine.Task{
		"tasks[3]": {
			{Name: "stop it", FQCN: "rb.stop", Params: map[string]any{"name": "svc"}},
			{Name: "then remove it", FQCN: "rb.delete", Params: map[string]any{"name": "svc"}},
		},
		"tasks[4]": {},
	}
	return rollback.Request{
		Target: target, Others: []rollback.Run{earlier, later}, Devices: inventoryOf("h1", "h2"),
		Authored: func(node string) ([]engine.Task, error) { return authored[node], nil },
	}
}

// TestRollbackPlanMatchesTheGolden pins what the planner refuses for the
// fixed request, and then the plan it builds once every refusal is accepted
// by name.
func TestRollbackPlanMatchesTheGolden(t *testing.T) {
	registerShapes(t)
	req := goldenRequest()
	_, err := rollback.Build(req)
	problems := refused(t, err)

	req.Leave = map[string]bool{"tasks[2]": true}
	req.AllowPartial = map[string]bool{"tasks[6]": true}
	req.AllowUnknown = map[string]bool{"tasks[5]": true}
	req.DespiteRuns = map[string]bool{"run-B": true}
	plan, err := rollback.Build(req)
	if err != nil {
		t.Fatalf("Build with every refusal accepted: %v", err)
	}
	golden.JSON(t, filepath.Join("testdata", "golden", "plan.json"), struct {
		Refused []rollback.Problem `json:"refused"`
		Plan    rollback.Plan      `json:"plan"`
	}{problems, plan})
}
