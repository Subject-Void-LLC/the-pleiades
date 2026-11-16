// Tests that a rollback's recorded values are data: the executor never
// renders a parameter its Undo names Literal, whether MarkLiteral set it on
// the task or the run reads it from WithRollback, and validation's helper
// leaves those parameters out.
package engine_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// literalRunbook is a rollback of one recorded undo: noop echoes its params,
// one holding every template marker as data, one an ordinary template.
const literalRunbook = `id: rollback-literal
tasks:
  - name: put the contact back
    noop:
      value: "noc {{ vars.site }} {% if true %}x{% endif %} {# a comment #}"
      fresh: "{{ vars.env }}"
`

// TestRollback_ARecordedValueIsNeverRendered: a parameter the run's Undo
// names Literal reaches the method exactly as recorded, by MarkLiteral or by
// WithRollback alone, while the task's other parameters still render; and
// LiteralFree leaves exactly those parameters out.
func TestRollback_ARecordedValueIsNeverRendered(t *testing.T) {
	const held = "noc {{ vars.site }} {% if true %}x{% endif %} {# a comment #}"
	undoes := map[string]engine.Undo{"tasks[0]": {Node: "tasks[0]", Literal: []string{"value"}}}
	for _, marked := range []bool{true, false} {
		dag := yamlRunbook(t, literalRunbook)
		if marked {
			engine.MarkLiteral(dag, undoes)
			if !reflect.DeepEqual(dag.Nodes["tasks[0]"].Literal, []string{"value"}) {
				t.Fatalf("MarkLiteral set %v", dag.Nodes["tasks[0]"].Literal)
			}
		}
		x := engine.NewExecutor(mapResolver{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
			engine.WithVariables(map[string]any{"env": "prod", "site": "lab"}), engine.WithRenderer(render.New()), engine.WithRollback("run-1", undoes))
		result, err := x.Run(context.Background(), dag)
		if err != nil || result.HasErrors() {
			t.Fatalf("marked=%v: Run: %v, %+v", marked, err, result.Nodes)
		}
		if got := result.Nodes[0].Stats["value"]; got != held {
			t.Errorf("marked=%v: the recorded value reached the method as %q", marked, got)
		}
		if got := result.Nodes[0].Stats["fresh"]; got != "prod" {
			t.Errorf("marked=%v: the task's own template rendered to %q", marked, got)
		}
	}
	params := map[string]any{"value": held, "fresh": "{{ vars.env }}"}
	if free := engine.LiteralFree(params, []string{"value"}); len(free) != 1 || free["fresh"] == nil {
		t.Errorf("LiteralFree = %v", free)
	}
	if free := engine.LiteralFree(params, nil); len(free) != 2 {
		t.Errorf("LiteralFree with nothing literal = %v", free)
	}
	engine.MarkLiteral(nil, undoes)
}
