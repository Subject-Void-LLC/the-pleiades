package engine_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
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

// TestMetadata_IsInterruptible is the table-driven proof of
// Metadata.IsInterruptible's own documented "nil means true" default: a
// plain bool could not represent "unset" as distinct from "explicitly
// false", which is exactly the distinction PLAN.md Section 16's
// interruptible: false exception depends on.
func TestMetadata_IsInterruptible(t *testing.T) {
	yes, no := true, false

	tests := []struct {
		name string
		in   engine.Metadata
		want bool
	}{
		{"nil pointer defaults to interruptible", engine.Metadata{Interruptible: nil}, true},
		{"explicit true", engine.Metadata{Interruptible: &yes}, true},
		{"explicit false", engine.Metadata{Interruptible: &no}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.IsInterruptible(); got != tt.want {
				t.Errorf("IsInterruptible() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestMetadata_Interruptible_JSONRoundTrip confirms Metadata.Interruptible
// round-trips through JSON as a real *bool, not silently collapsing
// "absent" and "explicitly false" into the same decoded value: an absent
// interruptible key decodes to a nil pointer (IsInterruptible() true), and
// an explicit false decodes to a non-nil pointer to false
// (IsInterruptible() false) -- two states a plain bool field could not
// distinguish.
func TestMetadata_Interruptible_JSONRoundTrip(t *testing.T) {
	no := false
	original := engine.WorkflowDef{
		ID:       "interruptible-false",
		Metadata: engine.Metadata{Interruptible: &no},
		Tasks:    []engine.Task{{Name: "a", FQCN: "noop"}},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal WorkflowDef: %v", err)
	}
	if !strings.Contains(string(data), `"interruptible":false`) {
		t.Fatalf("marshaled JSON %s does not contain an explicit \"interruptible\":false key", data)
	}

	var decoded engine.WorkflowDef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal WorkflowDef: %v", err)
	}
	if decoded.Metadata.IsInterruptible() {
		t.Error("expected IsInterruptible() to be false after round-tripping an explicit false through JSON")
	}
}

// TestMetadata_Interruptible_AbsentMeansTrue confirms a runbook with no
// metadata.interruptible key at all builds with Metadata.Interruptible
// nil, and therefore IsInterruptible() true, both from JSON and YAML --
// the safe default PLAN.md Section 16 requires (interruptible: false is
// the named exception, which only makes sense if the unmarked case is the
// common, abortable one).
func TestMetadata_Interruptible_AbsentMeansTrue(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	t.Run("JSON", func(t *testing.T) {
		payload := []byte(`{"id": "no-metadata", "tasks": [{"name": "a", "fqcn": "noop"}]}`)
		dag, err := builder.Build(payload)
		if err != nil {
			t.Fatalf("failed to build DAG: %v", err)
		}
		if dag.Metadata.Interruptible != nil {
			t.Errorf("expected Interruptible to be nil when absent, got %v", *dag.Metadata.Interruptible)
		}
		if !dag.Metadata.IsInterruptible() {
			t.Error("expected IsInterruptible() to default to true")
		}
	})

	t.Run("YAML", func(t *testing.T) {
		payload := []byte("id: no-metadata\ntasks:\n  - name: a\n    fqcn: noop\n")
		dag, err := builder.BuildFromYAML(payload)
		if err != nil {
			t.Fatalf("failed to build DAG: %v", err)
		}
		if dag.Metadata.Interruptible != nil {
			t.Errorf("expected Interruptible to be nil when absent, got %v", *dag.Metadata.Interruptible)
		}
		if !dag.Metadata.IsInterruptible() {
			t.Error("expected IsInterruptible() to default to true")
		}
	})

	t.Run("YAML explicit interruptible: false", func(t *testing.T) {
		payload := []byte("id: no-abort\nmetadata:\n  interruptible: false\ntasks:\n  - name: a\n    fqcn: noop\n")
		dag, err := builder.BuildFromYAML(payload)
		if err != nil {
			t.Fatalf("failed to build DAG: %v", err)
		}
		if dag.Metadata.Interruptible == nil || *dag.Metadata.Interruptible {
			t.Fatalf("expected Interruptible to decode to a non-nil false, got %v", dag.Metadata.Interruptible)
		}
		if dag.Metadata.IsInterruptible() {
			t.Error("expected IsInterruptible() to be false for an explicit interruptible: false")
		}
	})
}

// TestWorkflowDef_Hosts_JSONRoundTrip confirms WorkflowDef.Hosts round-trips
// through JSON, the same guarantee TestMetadata_ServiceEffecting_JSONRoundTrip
// establishes for Metadata.
func TestWorkflowDef_Hosts_JSONRoundTrip(t *testing.T) {
	original := engine.WorkflowDef{
		ID:    "hosts-default",
		Hosts: "sw1",
		Tasks: []engine.Task{{Name: "a", FQCN: "noop"}},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal WorkflowDef: %v", err)
	}

	var decoded engine.WorkflowDef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal WorkflowDef: %v", err)
	}

	if decoded.Hosts != "sw1" {
		t.Errorf("expected Hosts to round-trip as %q through JSON, got %q", "sw1", decoded.Hosts)
	}
}

// TestWorkflowDef_Hosts_YAMLRoundTrip is the YAML mirror of
// TestWorkflowDef_Hosts_JSONRoundTrip.
func TestWorkflowDef_Hosts_YAMLRoundTrip(t *testing.T) {
	original := engine.WorkflowDef{
		ID:    "hosts-default",
		Hosts: "sw1",
		Tasks: []engine.Task{{Name: "a", FQCN: "noop"}},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal WorkflowDef: %v", err)
	}

	var decoded engine.WorkflowDef
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal WorkflowDef: %v", err)
	}

	if decoded.Hosts != "sw1" {
		t.Errorf("expected Hosts to round-trip as %q through YAML, got %q", "sw1", decoded.Hosts)
	}
}

// TestDAGBuilder_Hosts confirms Build carries WorkflowDef.Hosts through to
// DAG.Hosts unchanged, and that an absent hosts: builds a DAG with an empty
// Hosts default, exactly today's behavior (every task must name its own
// target explicitly).
func TestDAGBuilder_Hosts(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	t.Run("hosts set", func(t *testing.T) {
		payload := []byte(`{"id": "with-hosts", "hosts": "sw1", "tasks": [{"name": "a", "fqcn": "noop"}]}`)
		dag, err := builder.Build(payload)
		if err != nil {
			t.Fatalf("failed to build DAG: %v", err)
		}
		if dag.Hosts != "sw1" {
			t.Errorf("expected dag.Hosts to be %q, got %q", "sw1", dag.Hosts)
		}
	})

	t.Run("hosts absent defaults to empty", func(t *testing.T) {
		payload := []byte(`{"id": "no-hosts", "tasks": [{"name": "a", "fqcn": "noop"}]}`)
		dag, err := builder.Build(payload)
		if err != nil {
			t.Fatalf("failed to build DAG: %v", err)
		}
		if dag.Hosts != "" {
			t.Errorf("expected dag.Hosts to default to empty, got %q", dag.Hosts)
		}
	})
}
