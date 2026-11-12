// Tests that a task's templated rollback: steps are journaled as the run
// rendered them on each device (journal_steps.go), through the real Executor
// and its journal projection: with the values the task ran with after a later
// set_fact changed them, and with no value and a reason where the rules a
// recorded undo is held to keep one out.
package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// stepsRunbook changes each device four times, each with a rollback: list,
// then a set_fact changes the variable the steps read.
const stepsRunbook = `id: steps
hosts: web
tasks:
  - name: pick
    set_fact:
      where: "{{ device.name }}-first"
  - name: kept
    rbtest.make: {name: one}
    rollback:
      - rbtest.unmake: {name: plain}
      - rbtest.unmake: {name: "{{ vars.where }}-kept"}
  - name: undeclared
    rbtest.make: {name: two}
    rollback:
      - rbtest.make: {name: "{{ vars.where }}"}
  - name: unrecorded
    rbtest.make: {name: three}
    rollback:
      - rbtest.unmake: {name: three, content: "{{ vars.where }}"}
  - name: secret
    rbtest.make: {name: four}
    rollback:
      - rbtest.unmake: {name: "{{ vars.where }}"}
  - name: repick
    set_fact:
      where: "{{ device.name }}-second"
`

// TestJournal_AuthoredStepsAreRenderedWithTheValuesTheTaskRanWith: each
// device's entry of a task holds its templated steps rendered with that
// device's values when the task ran, not the later set_fact's; a literal step
// is not recorded; a step through a method the task does not declare an undo
// through, a templated parameter the declaration does not record, and a value
// holding a known secret are each recorded with no value and their reason.
func TestJournal_AuthoredStepsAreRenderedWithTheValuesTheTaskRanWith(t *testing.T) {
	registerRollbackFixtures(t)
	journal := &entriesJournal{}
	actions := &undoable{}
	x := engine.NewExecutor(twoDevices(), factUndoable{actions, engine.NewBuiltinActionExecutor()}, lock.NewInProcessManager(), event.NewInProcessBus(),
		engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()), engine.WithJournal(journal),
		engine.WithKnownSecrets([]string{"web1-first"}))
	// The secret is web1's value of where, so on web1 every step reading it
	// is withheld for the secret first; web2 shows the other rules.
	result, err := x.Run(context.Background(), yamlRunbook(t, stepsRunbook))
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v %+v", err, result.Nodes)
	}
	steps := map[string][]engine.AuthoredStep{}
	for _, e := range journal.entries {
		steps[e.NodeID+"@"+e.DeviceID] = e.AuthoredSteps
	}

	kept := steps["tasks[1]@web2"]
	if len(kept) != 1 || kept[0].Index != 1 || kept[0].FQCN != "rbtest.unmake" || !kept[0].Complete || kept[0].Withheld != "" {
		t.Fatalf("web2's kept steps = %+v, want only step 1, complete", kept)
	}
	if len(kept[0].Params) != 1 || kept[0].Params[0].Key != "name" {
		t.Fatalf("web2's kept step params = %+v", kept[0].Params)
	}
	if v, _ := kept[0].Params[0].Value(); v != "web2-first-kept" {
		t.Errorf("web2's kept step journaled name %v, want web2-first-kept, the value when the task ran", v)
	}
	if want := engine.AuthoredStepDigest(engine.Task{FQCN: "rbtest.unmake", Params: map[string]any{"name": "{{ vars.where }}-kept"}}); kept[0].StepDigest != want {
		t.Errorf("step digest %s, want %s", kept[0].StepDigest, want)
	}

	for node, want := range map[string]engine.WithheldReason{
		"tasks[2]@web2": engine.WithheldUndeclared,
		"tasks[3]@web2": engine.WithheldValue,
		"tasks[4]@web2": "",
		"tasks[4]@web1": engine.WithheldSecret,
		"tasks[1]@web1": engine.WithheldSecret,
	} {
		got := steps[node]
		if len(got) != 1 {
			t.Errorf("%s: %d steps journaled, want 1: %+v", node, len(got), got)
			continue
		}
		if got[0].Withheld != want || got[0].Complete != (want == "") || (want != "" && len(got[0].Params) != 0) {
			t.Errorf("%s: step = %+v, want withheld %q with no value", node, got[0], want)
		}
	}
	for _, e := range journal.entries {
		if (e.NodeID == "tasks[0]" || e.NodeID == "tasks[5]") && len(e.AuthoredSteps) != 0 {
			t.Errorf("%s on %s journaled steps it does not have: %+v", e.NodeID, e.DeviceID, e.AuthoredSteps)
		}
	}
}
