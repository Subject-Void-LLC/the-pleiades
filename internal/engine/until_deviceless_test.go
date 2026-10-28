// Tests that an until task with no device (a controller-side call such as a
// lab node start) retries the method's own failure as one with a device does.
package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestUntil_ADevicelessTaskRetriesTheMethodsFailure(t *testing.T) {
	s := &stepper{fn: func(n int, _ *engine.Task) (engine.ActionResult, error) {
		if n < 2 {
			return engine.ActionResult{}, errors.New("[cml] 400: the node cannot start yet")
		}
		return engine.ActionResult{Stats: map[string]any{"state": "BOOTED"}}, nil
	}, builtin: engine.NewBuiltinActionExecutor()}
	x := engine.NewExecutor(mapResolver{}, s, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, `id: start
tasks:
  - rbtest.step: {note: x}
    register: started
    until: ["result.started.state == 'BOOTED'"]
    retries: 5
    delay: 0
  - rbtest.step: {note: after}
`))
	if err != nil {
		t.Fatal(err)
	}
	if result.HasErrors() || len(s.calls) != 4 {
		for _, n := range result.Nodes {
			t.Logf("%s%s err=%v retried=%v stage", n.NodeID, n.Iteration, n.Err, n.Retried)
		}
		t.Fatalf("calls %v; want three tries then the task after", s.calls)
	}
}

// TestUntil_TheAgentsNodeStartShape is the runbook a run-6 agent wrote: a
// device task, ignore_errors, and an until that tests for a field the failed
// try does not have.
func TestUntil_TheAgentsNodeStartShape(t *testing.T) {
	_, result, _ := runStepper(t, collection.ModeExecute, `id: start_node
hosts: web
tasks:
  - rbtest.step: {note: x}
    register: started
    ignore_errors: true
    until:
      - "has(result.started.lab_id)"
    retries: 3
    delay: 0
`, func(n int, _ *engine.Task) (engine.ActionResult, error) {
		if n < 2 {
			// As virt.cml.lab.start does: it records the lab it found, then
			// the controller refuses the start.
			return engine.ActionResult{Stats: map[string]any{"lab_id": "x"}}, errors.New("[cml] 400")
		}
		return engine.ActionResult{Stats: map[string]any{"lab_id": "x"}}, nil
	})
	for _, n := range result.Nodes {
		t.Logf("%s%s err=%v retried=%v ignored=%v", n.NodeID, n.Iteration, n.Err, n.Retried, n.Ignored)
	}
	if len(result.Nodes) != 3 || result.HasErrors() {
		t.Fatalf("want three tries ending in success, got %d results", len(result.Nodes))
	}
}
