// Tests that a block's rollback_on_failure renders a task's rollback: steps
// with the variables the task ran with (undo_vars.go), through the real
// Executor, after a later set_fact changed them.
package engine_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// factUndoable is undoable with set_fact run by the engine's own builtin
// executor, so a set_fact really sets its facts.
type factUndoable struct {
	*undoable
	builtin engine.ActionExecutor
}

// Execute implements engine.ActionExecutor.
func (f factUndoable) Execute(ctx context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	if task.FQCN == engine.SetFactName {
		return f.builtin.Execute(ctx, task, d)
	}
	return f.undoable.Execute(ctx, task, d)
}

// TestSetFact_AnUndoUsesTheValueItsTaskRanWith: a block's tasks are undone
// after a later set_fact changed the variable they read, and each undo still
// names what its task did: the recorded undo holds the rendered value the
// method returned, and an authored rollback: step renders with the variables
// its task ran with on that device, not the current ones, even when the task
// itself reads no variable. Before the fix the authored step rendered with the
// later value and removed a path the task never made.
func TestSetFact_AnUndoUsesTheValueItsTaskRanWith(t *testing.T) {
	registerRollbackFixtures(t)
	actions := &undoable{fail: map[string]bool{"boom@web1": true}}
	journal := &entriesJournal{}
	runbook := `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: pick
        set_fact:
          where: "{{ device.name }}-first"
      - name: recorded
        rbtest.make: {name: "{{ vars.where }}-recorded"}
      - name: authored
        rbtest.make: {name: "{{ vars.where }}-authored"}
        rollback:
          - rbtest.unmake: {name: "{{ vars.where }}-authored"}
      - name: plain
        rbtest.make: {name: plain}
        rollback:
          - rbtest.unmake: {name: "{{ vars.where }}-plain"}
      - name: repick
        set_fact:
          where: "{{ device.name }}-second"
      - name: boom
        rbtest.make: {name: "{{ vars.where }}-boom"}
    rescue:
      - name: r0
        noop: {}
`
	x := engine.NewExecutor(twoDevices(), factUndoable{actions, engine.NewBuiltinActionExecutor()}, lock.NewInProcessManager(), event.NewInProcessBus(),
		engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()), engine.WithJournal(journal))
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// plain reads no variable itself, and its undo still reads the device's
	// value from when plain ran.
	want := []string{"recorded", "authored", "plain", "boom", "unmake:web1-first-plain", "unmake:web1-first-authored", "unmake:web1-first-recorded", "r0"}
	if got := actions.calls("web1"); !slices.Equal(got, want) {
		t.Errorf("web1 ran %v, want %v", got, want)
	}
	if result.HasErrors() || !result.Rescued() {
		t.Errorf("HasErrors %v, Rescued %v: %+v", result.HasErrors(), result.Rescued(), result.Nodes)
	}
	// What a later pleiades rollback replays for the recorded undo is the
	// journal's rendered value, written when the task ran.
	found := 0
	for _, e := range journal.entries {
		if e.NodeID != "tasks[0].block[1]" || e.UndoesNode != "" {
			continue
		}
		found++
		if len(e.InverseParams) != 1 {
			t.Fatalf("%s's recorded undo holds %+v", e.DeviceID, e.InverseParams)
		}
		if v, _ := e.InverseParams[0].Value(); v != e.DeviceID+"-first-recorded" {
			t.Errorf("the journal holds %s's undo name as %v, want %s-first-recorded", e.DeviceID, v, e.DeviceID)
		}
	}
	if found != 2 {
		t.Errorf("the journal holds %d entries of the recorded task, want one per device", found)
	}
}
