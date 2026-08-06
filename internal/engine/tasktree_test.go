package engine_test

import (
	"encoding/json"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"go.yaml.in/yaml/v3"
)

// TestSynthesizedIDScheme confirms the synthesized node ID scheme exactly
// matches the contract's format for a runbook with pretasks, tasks
// (including a nested block/rescue/always), and posttasks: top-level
// entries are "pretasks[i]"/"tasks[i]"/"posttasks[i]" and a Block/Rescue/
// Always child appends ".block[j]"/".rescue[j]"/".always[j]" to its
// parent's ID, recursively.
func TestSynthesizedIDScheme(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "id-scheme",
		"pretasks": [
			{"name": "pre0", "fqcn": "noop"}
		],
		"tasks": [
			{"name": "t0", "fqcn": "noop"},
			{"name": "grouped", "block": [
				{"name": "b0", "fqcn": "noop"},
				{"name": "nested", "block": [
					{"name": "nb0", "fqcn": "noop"}
				]}
			], "rescue": [
				{"name": "r0", "fqcn": "noop"}
			], "always": [
				{"name": "a0", "fqcn": "noop"}
			]}
		],
		"posttasks": [
			{"name": "post0", "fqcn": "noop"}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}

	wantIDs := []string{
		"pretasks[0]",
		"tasks[0]",
		"tasks[1]",
		"tasks[1].block[0]",
		"tasks[1].block[1]",
		"tasks[1].block[1].block[0]",
		"tasks[1].rescue[0]",
		"tasks[1].always[0]",
		"posttasks[0]",
	}
	if len(dag.Nodes) != len(wantIDs) {
		t.Fatalf("expected %d synthesized IDs, got %d: %v", len(wantIDs), len(dag.Nodes), dag.Nodes)
	}
	for _, id := range wantIDs {
		if _, ok := dag.Nodes[id]; !ok {
			t.Errorf("expected synthesized ID %q to exist in dag.Nodes, got: %v", id, dag.Nodes)
		}
	}

	// Confirm each ID actually maps back to the Task it was synthesized
	// from, by Name, so the scheme is not just producing the right count
	// of the right-looking strings but the right pairing.
	wantNames := map[string]string{
		"pretasks[0]":                "pre0",
		"tasks[0]":                   "t0",
		"tasks[1]":                   "grouped",
		"tasks[1].block[0]":          "b0",
		"tasks[1].block[1]":          "nested",
		"tasks[1].block[1].block[0]": "nb0",
		"tasks[1].rescue[0]":         "r0",
		"tasks[1].always[0]":         "a0",
		"posttasks[0]":               "post0",
	}
	for id, wantName := range wantNames {
		node, ok := dag.Nodes[id]
		if !ok {
			continue // already reported above
		}
		if node.Name != wantName {
			t.Errorf("node %q: expected name %q, got %q", id, wantName, node.Name)
		}
	}
}

// TestSynthesizedIDScheme_Parallel confirms the synthesized ID scheme for
// a parallel task: children append ".parallel[j]" to their parent's ID,
// recursively, exactly mirroring ".block[j]"'s own scheme, and the
// synthetic fan-out/join markers append ".fanout"/".join".
func TestSynthesizedIDScheme_Parallel(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "id-scheme-parallel",
		"tasks": [
			{"name": "fanout", "parallel": [
				{"name": "p0", "fqcn": "noop"},
				{"name": "nested", "parallel": [
					{"name": "np0", "fqcn": "noop"}
				]}
			]}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build DAG: %v", err)
	}

	wantNames := map[string]string{
		"tasks[0]":                         "fanout",
		"tasks[0].parallel[0]":             "p0",
		"tasks[0].parallel[1]":             "nested",
		"tasks[0].parallel[1].parallel[0]": "np0",
	}
	for id, wantName := range wantNames {
		node, ok := dag.Nodes[id]
		if !ok {
			t.Errorf("expected synthesized ID %q to exist in dag.Nodes, got: %v", id, dag.Nodes)
			continue
		}
		if node.Name != wantName {
			t.Errorf("node %q: expected name %q, got %q", id, wantName, node.Name)
		}
	}

	for _, id := range []string{"tasks[0].fanout", "tasks[0].join", "tasks[0].parallel[1].fanout", "tasks[0].parallel[1].join"} {
		if _, ok := dag.Nodes[id]; !ok {
			t.Errorf("expected synthetic marker node %q to exist, got: %v", id, dag.Nodes)
		}
	}
}

// TestMetadata_ServiceEffecting_JSONRoundTrip confirms Metadata.
// ServiceEffecting round-trips through JSON: marshaling a WorkflowDef with
// it set and unmarshaling the result back preserves the value.
func TestMetadata_ServiceEffecting_JSONRoundTrip(t *testing.T) {
	original := engine.WorkflowDef{
		ID:       "svc-effecting",
		Metadata: engine.Metadata{ServiceEffecting: true},
		Tasks:    []engine.Task{{Name: "a", FQCN: "noop"}},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal WorkflowDef: %v", err)
	}

	var decoded engine.WorkflowDef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal WorkflowDef: %v", err)
	}

	if !decoded.Metadata.ServiceEffecting {
		t.Errorf("expected ServiceEffecting to round-trip as true through JSON, got false")
	}
}

// TestMetadata_ServiceEffecting_YAMLRoundTrip is the YAML mirror of
// TestMetadata_ServiceEffecting_JSONRoundTrip.
func TestMetadata_ServiceEffecting_YAMLRoundTrip(t *testing.T) {
	original := engine.WorkflowDef{
		ID:       "svc-effecting",
		Metadata: engine.Metadata{ServiceEffecting: true},
		Tasks:    []engine.Task{{Name: "a", FQCN: "noop"}},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal WorkflowDef: %v", err)
	}

	var decoded engine.WorkflowDef
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal WorkflowDef: %v", err)
	}

	if !decoded.Metadata.ServiceEffecting {
		t.Errorf("expected ServiceEffecting to round-trip as true through YAML, got false")
	}
}

// TestMetadata_AbsentSectionBuildsZeroValue confirms a runbook with no
// metadata section at all still builds successfully, with Metadata's zero
// value (ServiceEffecting false), both from JSON and YAML.
func TestMetadata_AbsentSectionBuildsZeroValue(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	t.Run("JSON", func(t *testing.T) {
		payload := []byte(`{"id": "no-metadata", "tasks": [{"name": "a", "fqcn": "noop"}]}`)
		dag, err := builder.Build(payload)
		if err != nil {
			t.Fatalf("failed to build DAG: %v", err)
		}
		if dag.Metadata.ServiceEffecting {
			t.Errorf("expected ServiceEffecting to default to false, got true")
		}
	})

	t.Run("YAML", func(t *testing.T) {
		payload := []byte("id: no-metadata\ntasks:\n  - name: a\n    fqcn: noop\n")
		dag, err := builder.BuildFromYAML(payload)
		if err != nil {
			t.Fatalf("failed to build DAG: %v", err)
		}
		if dag.Metadata.ServiceEffecting {
			t.Errorf("expected ServiceEffecting to default to false, got true")
		}
	})
}
