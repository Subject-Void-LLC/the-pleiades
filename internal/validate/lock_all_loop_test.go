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
// refuses in the run's own words, naming the cause the run names: iteration
// for a task that loops or retries (engine.ErrIterationLockAll), whatever it
// also reads, and the device read for one that only reads device
// (engine.ErrDeviceRootLockAll). What the run accepts validate accepts.
func TestLockAllOnATaskThatIterates_ValidateAndTheRunAgree(t *testing.T) {
	const head = "id: l\nhosts: web\ntasks:\n  - name: every lock\n    noop: {}\n"
	const deviceRead = "    when: [\"device.name != ''\"]\n"
	for _, tc := range []struct {
		name, keys string
		// reason is why both refuse the task, empty when both accept it.
		reason string
		// whole, when set, is the whole runbook, in place of head and keys.
		whole string
	}{
		{"a loop", "    lock_acquisition: all_at_plan_time\n    loop: [a, b]\n", engine.ErrIterationLockAll, ""},
		{"with_items", "    lock_acquisition: all_at_plan_time\n    with_items: [a, b]\n", engine.ErrIterationLockAll, ""},
		{"a loop written as an expression", "    lock_acquisition: all_at_plan_time\n    loop: \"{{ vars.names }}\"\n", engine.ErrIterationLockAll, ""},
		{"a retry", "    lock_acquisition: all_at_plan_time\n    register: r\n    until: [\"true\"]\n", engine.ErrIterationLockAll, ""},
		{"a loop that also reads device", "    lock_acquisition: all_at_plan_time\n    loop: [a, b]\n" + deviceRead, engine.ErrIterationLockAll, ""},
		{"a device read, no loop", "    lock_acquisition: all_at_plan_time\n" + deviceRead, engine.ErrDeviceRootLockAll, ""},
		// Inside a block with a rescue the run decides in another place
		// (runGuarded), and gives the same reason there.
		{name: "a loop inside a block with a rescue", reason: engine.ErrIterationLockAll, whole: "id: g\nhosts: web\ntasks:\n  - name: guarded\n    block:\n      - name: every lock\n        noop: {}\n        lock_acquisition: all_at_plan_time\n        loop: [a, b]\n    rescue:\n      - name: answer\n        noop: {}\n"},
		{"control: every lock, no loop", "    lock_acquisition: all_at_plan_time\n", "", ""},
		{"control: a loop, a lock per device", "    loop: [a, b]\n", "", ""},
		{"control: a retry, a lock per device", "    register: r\n    until: [\"true\"]\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runbook := head + tc.keys
			if tc.whole != "" {
				runbook = tc.whole
			}
			if strings.Contains(tc.keys, "vars.names") {
				runbook = strings.Replace(runbook, "tasks:\n", "vars:\n  names: [a, b]\ntasks:\n", 1)
			}
			dag := yamlDAG(t, runbook)

			// Plan time.
			var planned []string
			for _, f := range validate.TemplateRule(validate.WorldView{DAG: dag, Resolver: webDevices{}}) {
				planned = append(planned, f.Message)
			}
			if tc.reason == "" && len(planned) != 0 {
				t.Errorf("validate: findings %q, want none", planned)
			}
			if tc.reason != "" && (len(planned) != 1 || !strings.HasSuffix(planned[0], ": "+tc.reason)) {
				t.Errorf("validate: findings %q, want one ending in %q", planned, tc.reason)
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
			if tc.reason == "" && len(ran) != 0 {
				t.Errorf("run: node errors %q, want none", ran)
			}
			if tc.reason != "" && (len(ran) != 1 || !strings.HasSuffix(ran[0], ": "+tc.reason)) {
				t.Errorf("run: node errors %q, want one ending in %q", ran, tc.reason)
			}
			if tc.reason != "" && action.calls.Load() != 0 {
				t.Errorf("a refused task still ran %d times", action.calls.Load())
			}
			// The same words: validate's finding is the run's node error with
			// the word task before it.
			if len(planned) != len(ran) || (len(ran) == 1 && planned[0] != "task "+ran[0]) {
				t.Errorf("validate and the run must say the same thing\nvalidate: %q\nrun: %q", planned, ran)
			}
		})
	}
}
