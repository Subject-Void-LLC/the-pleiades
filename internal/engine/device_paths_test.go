// Tests of the failure branches of the paths that run a task per device or
// per unit: a task reading device with no hosts, a guarded task whose target
// resolves nowhere, a unit's task that fails to render or resolves nowhere,
// and an authored undo step that fails to render.
package engine_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// runWith runs runbook on twoDevices with actions and a renderer.
func runWith(t *testing.T, actions engine.ActionExecutor, runbook string) engine.RunResult {
	t.Helper()
	x := engine.NewExecutor(twoDevices(), actions, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestDevicePaths_FailureBranches(t *testing.T) {
	registerRollbackFixtures(t)

	// A task reading device with no hosts runs once, on no device, reading
	// an empty device root.
	none := &undoable{}
	result := runWith(t, none, "id: n\ntasks:\n  - name: alone\n    rbtest.make: {name: x}\n    when: [\"size(device) == 0\"]\n")
	if result.HasErrors() || !slices.Contains(none.calls(""), "alone") {
		t.Errorf("no hosts: ran %v, %s", none.calls(""), nodeErrs(result))
	}

	// A task in a block with a rescue, whose target resolves nowhere.
	r := runWith(t, &undoable{}, "id: g\nhosts: nosuch\ntasks:\n  - block:\n      - rbtest.make: {name: x}\n    rescue:\n      - noop: {}\n")
	if !strings.Contains(nodeErrs(r), "failed to resolve target") {
		t.Errorf("a guarded task with no target: %s", nodeErrs(r))
	}

	// A unit's task that cannot render, and one whose target resolves
	// nowhere.
	for name, tc := range map[string]struct{ task, want string }{
		"a render that fails":            {"rbtest.make: {name: x, note: \"{{ vars.nosuch }}\"}", "failed to render"},
		"a target that resolves nowhere": {"rbtest.make: {name: x, target: nosuch}", "failed to resolve target"},
	} {
		r := runWith(t, &undoable{}, "id: u\nhosts: web\ntasks:\n  - block:\n      - "+tc.task+"\n      - rbtest.make: {name: y, target: web}\n    rescue:\n      - noop: {}\n")
		if !strings.Contains(nodeErrs(r), tc.want) {
			t.Errorf("%s: %s", name, nodeErrs(r))
		}
	}

	// An authored undo step that cannot render fails the undo, so the rescue
	// is not reached.
	failing := &undoable{fail: map[string]bool{"b1@web1": true}}
	r = runWith(t, failing, `id: f
hosts: web
tasks:
  - name: guarded
    rollback_on_failure: true
    block:
      - name: b0
        rbtest.make: {name: zero}
        rollback:
          - name: "undo"
            rbtest.make: {name: "{{ vars.nosuch }}"}
      - name: b1
        rbtest.make: {name: one}
    rescue:
      - name: r0
        noop: {}
`)
	if !r.HasErrors() || slices.Contains(failing.calls("web1"), "r0") {
		t.Errorf("an authored undo that cannot render: web1 ran %v, %s", failing.calls("web1"), nodeErrs(r))
	}
}
