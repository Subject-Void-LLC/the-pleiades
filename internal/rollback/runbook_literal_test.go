// Tests that a rollback writes back what the undone run recorded, exactly:
// a recorded value holding {{, {% or {# is data a device held, and the
// rollback runbook's engine never renders it.
package rollback_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// noDevices resolves every target to nothing, which the rollback runbook's
// device-less tasks never ask for.
type noDevices struct{}

// Resolve implements engine's target resolver.
func (noDevices) Resolve(string) []inventory.InventoryItem { return nil }

// TestRunbook_ARecordedValueIsWrittenBackAsRecorded: every parameter of a
// recorded undo, and an authored step's values the run rendered, reach the
// method as recorded, each template marker included, while an authored
// step's own template still renders. noop echoes its params into its stats.
func TestRunbook_ARecordedValueIsWrittenBackAsRecorded(t *testing.T) {
	const held = "noc {{ vars.env }} {% if true %}x{% endif %} {# a comment #}"
	levels := [][]rollback.Step{
		{{Node: "tasks[0]", FQCN: "noop", Name: "put the contact back", Source: rollback.SourceRecorded,
			Params: map[string]any{"value": held, "type": "octet-string"}}},
		{{Node: "tasks[1]", Index: 0, FQCN: "noop", Name: "authored undo", Source: rollback.SourceAuthored, Rendered: []string{"kept"},
			Params: map[string]any{"kept": held, "fresh": "{{ vars.env }}"}}},
	}
	payload, undoes, err := rollback.Runbook("rollback-run-1", levels, false)
	if err != nil {
		t.Fatal(err)
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML(payload)
	if err != nil {
		t.Fatalf("the rollback runbook does not build: %v\n%s", err, payload)
	}
	x := engine.NewExecutor(noDevices{}, engine.NewBuiltinActionExecutor(), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0,
		engine.WithVariables(map[string]any{"env": "prod"}), engine.WithRenderer(render.New()), engine.WithRollback("run-1", undoes))
	result, err := x.Run(context.Background(), dag)
	if err != nil || result.HasErrors() {
		t.Fatalf("Run: %v, %+v", err, result.Nodes)
	}
	if got := result.Nodes[0].Stats["value"]; got != held {
		t.Errorf("a recorded value reached the method as %q, want %q", got, held)
	}
	if got := result.Nodes[1].Stats["kept"]; got != held {
		t.Errorf("an authored step's rendered value reached the method as %q, want %q", got, held)
	}
	if got := result.Nodes[1].Stats["fresh"]; got != "prod" {
		t.Errorf("an authored step's own template rendered to %q, want prod", got)
	}
}
