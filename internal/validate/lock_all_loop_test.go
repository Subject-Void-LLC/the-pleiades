// Package validate_test: validate and the run agree on which tasks may take
// lock_acquisition: all_at_plan_time. A task that loops or retries runs on
// each device on its own, so the run refuses to take every device's lock
// together for it, and validate refuses the same runbook before anything
// runs, in the same words.
package validate_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// countingAction is an action executor that only counts its calls.
type countingAction struct{ calls atomic.Int64 }

// Execute implements engine.ActionExecutor.
func (c *countingAction) Execute(context.Context, *engine.Task, inventory.InventoryItem) (engine.ActionResult, error) {
	c.calls.Add(1)
	return engine.ActionResult{}, nil
}

// webDevices resolves the target web to two active devices, and anything
// else to none.
type webDevices struct{}

// Resolve implements engine.TargetResolver.
func (webDevices) Resolve(target string) []inventory.InventoryItem {
	if target != "web" {
		return nil
	}
	return []inventory.InventoryItem{
		&inventorytest.Stub{StubID: "web1", StubName: "web1", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "web2", StubName: "web2", StubState: inventory.StateActive},
	}
}

// TestLockAllOnATaskThatIterates_ValidateAndTheRunAgree builds each runbook
// once and asks both: validate's TemplateRule, and a real run through the
// executor. Whatever the run refuses for taking every lock together, validate
// refuses with the same reason (engine.ErrDeviceRootLockAll), and what the run
// accepts validate accepts.
func TestLockAllOnATaskThatIterates_ValidateAndTheRunAgree(t *testing.T) {
	const head = "id: l\nhosts: web\ntasks:\n  - name: every lock\n    noop: {}\n"
	for _, tc := range []struct {
		name, keys string
		refused    bool
	}{
		{"a loop", "    lock_acquisition: all_at_plan_time\n    loop: [a, b]\n", true},
		{"with_items", "    lock_acquisition: all_at_plan_time\n    with_items: [a, b]\n", true},
		{"a loop written as an expression", "    lock_acquisition: all_at_plan_time\n    loop: \"{{ vars.names }}\"\n", true},
		{"a retry", "    lock_acquisition: all_at_plan_time\n    register: r\n    until: [\"true\"]\n", true},
		{"control: every lock, no loop", "    lock_acquisition: all_at_plan_time\n", false},
		{"control: a loop, a lock per device", "    loop: [a, b]\n", false},
		{"control: a retry, a lock per device", "    register: r\n    until: [\"true\"]\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runbook := head + tc.keys
			if strings.Contains(tc.keys, "vars.names") {
				runbook = strings.Replace(runbook, "tasks:\n", "vars:\n  names: [a, b]\ntasks:\n", 1)
			}
			dag := yamlDAG(t, runbook)

			// Plan time.
			var planned []string
			for _, f := range validate.TemplateRule(validate.WorldView{DAG: dag, Resolver: webDevices{}}) {
				planned = append(planned, f.Message)
			}
			planRefused := len(planned) == 1 && strings.Contains(planned[0], engine.ErrDeviceRootLockAll)
			if planRefused != tc.refused || (!tc.refused && len(planned) != 0) {
				t.Errorf("validate: findings %q, want refused=%v", planned, tc.refused)
			}

			// The run itself, on the same compiled runbook.
			action := &countingAction{}
			x := engine.NewExecutor(webDevices{}, action, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
				engine.WithRenderer(render.New()))
			result, err := x.Run(context.Background(), dag)
			if err != nil {
				t.Fatal(err)
			}
			var ran []string
			for _, n := range result.Nodes {
				if n.Err != nil {
					ran = append(ran, n.Err.Error())
				}
			}
			runRefused := len(ran) == 1 && strings.Contains(ran[0], engine.ErrDeviceRootLockAll)
			if runRefused != tc.refused || (!tc.refused && len(ran) != 0) {
				t.Errorf("run: node errors %q, want refused=%v", ran, tc.refused)
			}
			if tc.refused && action.calls.Load() != 0 {
				t.Errorf("a refused task still ran %d times", action.calls.Load())
			}
			if planRefused != runRefused {
				t.Errorf("validate refused=%v and the run refused=%v: they must agree\nvalidate: %q\nrun: %q", planRefused, runRefused, planned, ran)
			}
		})
	}
}
