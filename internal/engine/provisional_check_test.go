// Tests of WithProvisionalChecks: in check mode, a provisional node whose
// check fails is reported unchecked, naming why, rather than failing the
// run; a node not named fails as before; and outside check mode the option
// changes nothing.
package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// failingChecks checks every task, failing the check of the one named second.
type failingChecks struct{ undoable }

func (f *failingChecks) Check(_ context.Context, task *engine.Task, _ inventory.InventoryItem) (engine.ActionResult, error) {
	if task.Name == "second" {
		return engine.ActionResult{}, errors.New("the link is already stopped")
	}
	return engine.ActionResult{Changed: true}, nil
}

const provisionalRunbook = `id: undo
hosts: web
tasks:
  - name: first
    rbtest.make: {name: one}
  - name: second
    rbtest.make: {name: two}
`

func TestProvisionalChecks_AFailedProvisionalCheckIsUnchecked(t *testing.T) {
	registerRollbackFixtures(t)
	run := func(opts ...engine.ExecutorOption) engine.RunResult {
		t.Helper()
		opts = append([]engine.ExecutorOption{engine.WithRenderer(render.New())}, opts...)
		x := engine.NewExecutor(twoDevices(), &failingChecks{}, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
		result, err := x.Run(context.Background(), yamlRunbook(t, provisionalRunbook))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		return result
	}
	provisional := engine.WithProvisionalChecks(map[string]string{"tasks[1]": "an earlier step changes web first"})

	result := run(engine.WithMode(collection.ModeCheck), provisional)
	if result.HasErrors() {
		t.Fatalf("a provisional check's failure failed the run: %+v", result.Nodes)
	}
	unchecked := 0
	for _, n := range result.Nodes {
		if n.NodeID == "tasks[1]" {
			if !n.Unchecked || !strings.Contains(n.SkipReason, "provisional: an earlier step changes web first") || !strings.Contains(n.SkipReason, "already stopped") {
				t.Errorf("tasks[1] = %+v, want it unchecked as provisional, with the check's own words", n)
			}
			unchecked++
		}
		if n.NodeID == "tasks[0]" && n.Unchecked {
			t.Errorf("tasks[0], not provisional, is unchecked: %+v", n)
		}
	}
	if unchecked != 2 {
		t.Errorf("%d devices reported tasks[1], want 2", unchecked)
	}

	if result := run(engine.WithMode(collection.ModeCheck)); !result.HasErrors() {
		t.Error("without the option, a failed check did not fail the run")
	}
}
