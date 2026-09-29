// Tests for within:, the bound on a target rendered from data (Phase 117a),
// from the builder's rules to resolution through the real Executor.
package engine_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestWithin_BuilderRules: a rendered target needs within:, within: needs a
// rendered target, and within: itself is never rendered.
func TestWithin_BuilderRules(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, task, want string
	}{
		{"a rendered target without within", "noop:\n      target: \"{{ vars.sw }}\"", "needs a within:"},
		{"within without a rendered target", "noop:\n      target: sw1\n    within: switches", "without a rendered params.target"},
		{"within with no target at all", "noop: {}\n    within: switches", "without a rendered params.target"},
		{"a rendered within", "noop:\n      target: \"{{ vars.sw }}\"\n    within: \"{{ vars.bound }}\"", "written literally"},
		{"both, well formed", "noop:\n      target: \"{{ vars.sw }}\"\n    within: switches", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := engine.NewBuilder(eval).BuildFromYAML([]byte("id: w\ntasks:\n  - name: t\n    " + tc.task + "\n"))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused a well-formed task: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("error = %v, want one saying %q", err, tc.want)
			}
		})
	}
}

// TestWithin_TaskTargetIsTheBoundBeforeRendering: plan-time checks, which
// read TaskTarget, see every device the data could name.
func TestWithin_TaskTargetIsTheBoundBeforeRendering(t *testing.T) {
	task := &engine.Task{FQCN: "noop", Within: "switches", Params: map[string]any{"target": "{{ vars.sw }}"}}
	if got := engine.TaskTarget(&engine.DAG{Hosts: "all"}, task); got != "switches" {
		t.Errorf("TaskTarget() = %q, want the bound switches", got)
	}
}

// TestWithin_ResolvesOnlyNamesInsideTheBound: through the real Executor, a
// rendered name inside the bound runs there, several names run on each, a
// name outside the bound and a tag fail naming both, and nothing falls back
// to hosts:.
func TestWithin_ResolvesOnlyNamesInsideTheBound(t *testing.T) {
	sw1 := &inventorytest.Stub{StubID: "sw1", StubName: "sw1", StubState: inventory.StateActive}
	sw2 := &inventorytest.Stub{StubID: "sw2", StubName: "sw2", StubState: inventory.StateActive}
	dc := &inventorytest.Stub{StubID: "dc1", StubName: "dc1", StubState: inventory.StateActive}
	resolver := mapResolver{
		"switches": {sw1, sw2},
		"sw1":      {sw1}, "sw2": {sw2}, "dc1": {dc},
		"everything": {sw1, sw2, dc},
	}
	runbook := "id: w\nhosts: everything\ntasks:\n  - name: collect\n    noop:\n      target: \"{{ vars.sw }}\"\n    within: switches\n"
	for _, tc := range []struct {
		name     string
		sw       any
		want     []string
		failWith string
	}{
		{"one name inside", "sw1", []string{"sw1"}, ""},
		{"several, as text", "sw1, sw2", []string{"sw1", "sw2"}, ""},
		{"several, as a list", []any{"sw2", "sw1"}, []string{"sw1", "sw2"}, ""},
		{"a name outside", "dc1", nil, `"dc1", which is not a device within "switches"`},
		{"the tag itself", "switches", nil, `"switches", which is not a device within "switches"`},
		{"nothing", " , ", nil, "names no device"},
		{"a number", 7, nil, "must be a device name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := engine.NewExecutor(resolver, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
				engine.WithVariables(map[string]any{"sw": tc.sw}), engine.WithRenderer(render.New()))
			result, err := x.Run(context.Background(), yamlRunbook(t, runbook))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if tc.failWith != "" {
				if len(result.Nodes) != 1 || result.Nodes[0].Err == nil || !strings.Contains(result.Nodes[0].Err.Error(), tc.failWith) {
					t.Fatalf("want one failure saying %s, got %+v", tc.failWith, result.Nodes)
				}
				return
			}
			var ran []string
			for _, n := range result.Nodes {
				if n.Err != nil {
					t.Fatalf("a device failed: %v", n.Err)
				}
				ran = append(ran, n.Device)
			}
			sort.Strings(ran)
			if strings.Join(ran, ",") != strings.Join(tc.want, ",") {
				t.Errorf("ran on %v, want %v", ran, tc.want)
			}
		})
	}
}
