package engine_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

func TestDAGBuilder_ReleaseGate(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	t.Run("Valid DAG", func(t *testing.T) {
		payload := []byte(`{
			"id": "playbook-1",
			"nodes": [
				{"id": "A", "action": "START"},
				{"id": "B", "action": "PING"},
				{"id": "C", "action": "REBOOT"}
			],
			"edges": [
				{"from": "A", "to": "B"},
				{"from": "B", "to": "C", "condition": "stat.ping_ms > 100"}
			]
		}`)

		dag, err := builder.Build(payload)
		if err != nil {
			t.Fatalf("failed to build valid DAG: %v", err)
		}
		if len(dag.Nodes) != 3 {
			t.Errorf("expected 3 nodes, got %d", len(dag.Nodes))
		}
		
		// verify CEL compilation occurred
		if len(dag.Adjacency["B"]) != 1 {
			t.Fatalf("expected 1 edge from B")
		}
		if dag.Adjacency["B"][0].Condition == nil {
			t.Errorf("expected condition to be compiled for B->C edge")
		}
	})

	t.Run("Circular Dependency", func(t *testing.T) {
		payload := []byte(`{
			"id": "playbook-cycle",
			"nodes": [
				{"id": "A", "action": "STEP_1"},
				{"id": "B", "action": "STEP_2"},
				{"id": "C", "action": "STEP_3"}
			],
			"edges": [
				{"from": "A", "to": "B"},
				{"from": "B", "to": "C"},
				{"from": "C", "to": "A"} 
			]
		}`) // C -> A creates a cycle

		_, err := builder.Build(payload)
		if err == nil {
			t.Fatalf("expected build to fail on circular dependency")
		}
		if err.Error() != "circular dependency detected in workflow DAG" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}
