// Tests for rendered task parameters (Phase 117a) through the real
// Executor: a later task's params read vars and earlier results, typed, and
// a run with no renderer refuses rather than passing the literal text on.
package engine_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// renderRunbook registers a ticket-shaped result, then renders a second
// task's params from it and from vars. noop echoes its params into its
// result, so the second task's registered stats are exactly what rendered.
const renderRunbook = `id: render
tasks:
  - name: ticket
    noop:
      number: INC1
      ci: core-sw1
      tags:
        - a
        - b
    register: ticket
  - name: use it
    noop:
      path: "/api/{{ result.ticket.number | urlencode }}/{{ vars.env }}"
      ci: "{{ result.ticket.ci }}"
      tags: "{{ result.ticket.tags }}"
      nested:
        list:
          - "{{ vars.env | upper }}"
          - plain
      untouched: no template here
    register: used
`

// yamlRunbook compiles a YAML runbook through the real Builder, the path a
// runbook file takes.
func yamlRunbook(t *testing.T, payload string) *engine.DAG {
	t.Helper()
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(payload))
	if err != nil {
		t.Fatalf("building the runbook: %v", err)
	}
	return dag
}

// TestRenderedParams_ReadVarsAndEarlierResults: text renders to text, a
// parameter that is one expression keeps its type (a list stays a list),
// nested maps and lists render, and a value with no template is untouched.
func TestRenderedParams_ReadVarsAndEarlierResults(t *testing.T) {
	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithVariables(map[string]any{"env": "prod"}), engine.WithRenderer(render.New()))
	result, err := x.Run(context.Background(), yamlRunbook(t, renderRunbook))
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	got := result.Nodes[1].Stats
	want := map[string]any{
		"path":      "/api/INC1/prod",
		"ci":        "core-sw1",
		"tags":      []any{"a", "b"},
		"nested":    map[string]any{"list": []any{"PROD", "plain"}},
		"untouched": "no template here",
	}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("rendered %s = %#v, want %#v", k, got[k], v)
		}
	}
}

// TestRenderedParams_FailBeforeTheAction: an undefined name, and a run
// with no renderer, fail the task at the render stage, naming why, and the
// action never runs.
func TestRenderedParams_FailBeforeTheAction(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []engine.ExecutorOption
		want string
	}{
		{"undefined variable", []engine.ExecutorOption{engine.WithRenderer(render.New())}, "vars.env"},
		{"no renderer", nil, "no template renderer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0, tc.opts...)
			result, err := x.Run(context.Background(), yamlRunbook(t, renderRunbook))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			var failed *engine.NodeResult
			for i := range result.Nodes {
				if result.Nodes[i].Err != nil {
					failed = &result.Nodes[i]
				}
			}
			if failed == nil || failed.NodeID != "tasks[1]" || !strings.Contains(failed.Err.Error(), tc.want) {
				t.Fatalf("want tasks[1] failed naming %q, got %+v", tc.want, result.Nodes)
			}
			if failed.Stats != nil {
				t.Errorf("the action ran and reported %v", failed.Stats)
			}
		})
	}
}

// TestRenderedParams_HoldTheRulesWithoutValidation: the executor holds a
// template to the same rules validation does, so a DAG that reached it
// unvalidated still cannot render data onto a command line unquoted or into
// a request's host. The action never runs.
func TestRenderedParams_HoldTheRulesWithoutValidation(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "t117e.shell", Manifest: collection.Manifest{Status: collection.StatusDeclared,
		Doc: collection.Doc{Params: []collection.Param{{Name: "cmd", Type: "string", Format: collection.ParamFormatCommand}, {Name: "url", Type: "string", Format: collection.ParamFormatURL}}}}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ param, value, want string }{
		{"cmd", "echo {{ vars.x }}", "command text"},
		{"url", "https://{{ vars.x }}/api", "is a URL"},
	} {
		dag := yamlRunbook(t, "id: r\ntasks:\n  - name: unsafe\n    t117e.shell:\n      "+tc.param+": \""+tc.value+"\"\n")
		x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
			engine.WithVariables(map[string]any{"x": "y; reboot"}), engine.WithRenderer(render.New()))
		result, err := x.Run(context.Background(), dag)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if n := result.Nodes[0]; n.Err == nil || !strings.Contains(n.Err.Error(), tc.want) || !strings.Contains(n.Err.Error(), "render") {
			t.Errorf("%s: node %+v, want a render failure saying %q", tc.param, n, tc.want)
		}
	}
}

// TestConditionsReadResult: a condition reads a register one device wrote
// as result.<register>, the same root rendered params have, so an author
// need not index stat by a device id; a register several devices wrote is
// not there, and has() says so.
func TestConditionsReadResult(t *testing.T) {
	dag := yamlRunbook(t, `id: c
tasks:
  - name: incident
    noop:
      priority: "1 - Critical"
    register: incident
  - name: critical
    noop:
      changed: true
    when_cel: "result.incident.priority == '1 - Critical'"
  - name: moderate
    noop:
      changed: true
    when_cel: "result.incident.priority == '3 - Moderate'"
  - name: absent
    noop:
      changed: true
    when_cel: "has(result.nothing)"
`)
	x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
	result, err := x.Run(context.Background(), dag)
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	got := map[string]bool{}
	for _, n := range result.Nodes[1:] {
		got[n.NodeID] = n.Skipped
	}
	if got["tasks[1]"] || !got["tasks[2]"] || !got["tasks[3]"] {
		t.Errorf("skipped = %v, want only the critical task to run", got)
	}
}
