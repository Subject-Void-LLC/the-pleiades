// Tests that a task inside a block with a rescue:, an always: or
// rollback_on_failure reads the variables its device brings (group_vars and
// host_vars, folded into the device's vars property) exactly as a top-level
// task does, through the real Executor: in the block, the rescue and the
// always, in a nested block, in a loop, in a condition, and in an undo step.
package engine_test

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// notesSeen records each method call's rendered note as "<task>@<device>:
// <note>", and runs the engine's own actions (fail, noop, set_fact) as the
// platform does, so a fail task really fails its block.
type notesSeen struct {
	builtin engine.ActionExecutor
	mu      sync.Mutex
	seen    []string
}

// Execute implements engine.ActionExecutor.
func (n *notesSeen) Execute(ctx context.Context, task *engine.Task, d inventory.InventoryItem) (engine.ActionResult, error) {
	switch task.FQCN {
	case engine.FailName, engine.SetFactName, "noop":
		return n.builtin.Execute(ctx, task, d)
	}
	note, _ := task.Params["note"].(string)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, task.Name+"@"+d.Name()+": "+note)
	return engine.ActionResult{}, nil
}

// runGuardedVars runs runbook on two devices, a bringing role hub and b
// bringing role spoke, and returns every call's note, sorted, and the result.
func runGuardedVars(t *testing.T, runbook string, opts ...engine.ExecutorOption) ([]string, engine.RunResult) {
	t.Helper()
	varsFixtures.Do(func() {
		registerRollbackFixtures(t)
		registerDeviceRootTypes()
	})
	seen := &notesSeen{builtin: engine.NewBuiltinActionExecutor()}
	resolver := varsDevices(map[string]any{"role": "hub"}, map[string]any{"role": "spoke"})
	opts = append([]engine.ExecutorOption{engine.WithRenderer(render.New())}, opts...)
	x := engine.NewExecutor(resolver, seen, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, opts...)
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	slices.Sort(seen.seen)
	return seen.seen, result
}

// TestVars_EveryPartOfAGuardedBlockReadsTheDevicesVars: run 10's defect. A
// task reading a variable only its device brings rendered at the top level
// and failed with "no value supplied for vars.role" once it sat in a block
// with a rescue, an always or rollback_on_failure, in the block's own tasks
// and its rescue alike, though validate passed it.
func TestVars_EveryPartOfAGuardedBlockReadsTheDevicesVars(t *testing.T) {
	cases := []struct {
		name, runbook string
		want          []string
		// extra is the run's extra variables, nil for none.
		extra map[string]any
		// rescued says the block failed on purpose and its rescue answered.
		rescued bool
	}{
		{
			name: "block, rescue and always",
			runbook: `id: guarded
hosts: web
tasks:
  - name: guarded
    block:
      - name: in-block
        rbtest.make:
          name: x
          note: "{{ vars.role }}"
      - name: break it
        fail:
          msg: on purpose
    rescue:
      - name: in-rescue
        rbtest.make:
          name: x
          note: "{{ vars.role }}"
    always:
      - name: in-always
        rbtest.make:
          name: x
          note: "{{ vars.role }}"
`,
			want: []string{
				"in-always@a: hub", "in-always@b: spoke", "in-block@a: hub", "in-block@b: spoke",
				"in-rescue@a: hub", "in-rescue@b: spoke",
			},
			rescued: true,
		},
		{
			name: "an always alone, and a condition reading the device's vars",
			runbook: `id: always
hosts: web
tasks:
  - name: guarded
    block:
      - name: hub-only
        when: ["vars.role == 'hub'"]
        rbtest.make:
          name: x
          note: "{{ vars.role }}"
    always:
      - name: in-always
        rbtest.make:
          name: x
          note: "{{ vars.role }}"
`,
			want: []string{"hub-only@a: hub", "in-always@a: hub", "in-always@b: spoke"},
		},
		{
			name: "a nested block and a loop inside a rescue",
			runbook: `id: nested
hosts: web
tasks:
  - name: outer
    block:
      - name: inner
        block:
          - name: nested
            rbtest.make:
              name: x
              note: "{{ vars.role }}"
          - name: break it
            fail:
              msg: on purpose
    rescue:
      - name: looped
        loop: [1, 2]
        rbtest.make:
          name: x
          note: "{{ vars.role }}-{{ item }}"
`,
			want: []string{
				"looped@a: hub-1", "looped@a: hub-2", "looped@b: spoke-1", "looped@b: spoke-2",
				"nested@a: hub", "nested@b: spoke",
			},
			rescued: true,
		},
		{
			name: "a block with rollback_on_failure",
			runbook: `id: rollback
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: in-block
        rbtest.make:
          name: x
          note: "{{ vars.role }}"
`,
			want: []string{"in-block@a: hub", "in-block@b: spoke"},
		},
		{
			name: "a target rendered from data, in a block and at the top level",
			runbook: `id: within
tasks:
  - name: top
    within: web
    rbtest.make:
      name: x
      target: "{{ vars.which }}"
      note: "{{ vars.role }}"
  - name: guarded
    block:
      - name: in-block
        within: web
        rbtest.make:
          name: x
          target: "{{ vars.which }}"
          note: "{{ vars.role }}"
    always:
      - name: in-always
        within: web
        rbtest.make:
          name: x
          target: "{{ vars.which }}"
          note: "{{ vars.role }}"
`,
			want:  []string{"in-always@b: spoke", "in-block@b: spoke", "top@b: spoke"},
			extra: map[string]any{"which": "b"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var opts []engine.ExecutorOption
			if c.extra != nil {
				opts = append(opts, engine.WithVariables(c.extra))
			}
			seen, result := runGuardedVars(t, c.runbook, opts...)
			for _, n := range result.Nodes {
				// The fail task is the one failure these runbooks mean.
				if n.Err != nil && !c.rescued {
					t.Errorf("%s on %s failed: %v", n.NodeID, n.Device, n.Err)
				}
			}
			if c.rescued && !result.Rescued() {
				t.Errorf("the rescue did not answer the failure: %+v", result.Nodes)
			}
			if !slices.Equal(seen, c.want) {
				t.Errorf("saw %v, want %v", seen, c.want)
			}
		})
	}
}

// TestVars_AnUndoStepReadsTheDevicesVars: a task in a rollback_on_failure
// block renders its rollback: step with the variables it ran with, its
// device's own among them, so the undo removes what the task made there.
func TestVars_AnUndoStepReadsTheDevicesVars(t *testing.T) {
	varsFixtures.Do(func() {
		registerRollbackFixtures(t)
		registerDeviceRootTypes()
	})
	actions := &undoable{fail: map[string]bool{"b1@a": true}}
	result := runUndoable(t, `id: rb
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: "{{ vars.role }}"}
        rollback:
          - rbtest.unmake: {name: "undo-{{ vars.role }}"}
      - name: b1
        rbtest.make: {name: one}
    rescue:
      - name: r0
        noop: {}
`, varsDevices(map[string]any{"role": "hub"}, map[string]any{"role": "spoke"}), actions)
	if got, want := actions.calls("a"), []string{"b0", "b1", "unmake:undo-hub", "r0"}; !slices.Equal(got, want) {
		t.Errorf("a ran %v, want %v (%+v)", got, want, result.Nodes)
	}
	if got, want := actions.calls("b"), []string{"b0", "b1"}; !slices.Equal(got, want) {
		t.Errorf("b ran %v, want %v", got, want)
	}
	if !result.Rescued() {
		t.Errorf("the rescue did not answer b1's failure: %+v", result.Nodes)
	}
}
