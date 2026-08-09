package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestTaskKind_Classification confirms Task.Kind classifies every shape it
// is meant to distinguish: exactly one of FQCN/Block/Parallel set is that
// kind, and none or more than one set is TaskKindInvalid. Synthetic nodes
// (TaskKindSynthetic) are Builder-internal (registerSyntheticNode,
// tasktree.go), not constructible from this external test package, and
// are covered instead by the parallel-fan-out/join tests in dag_test.go,
// which observe their effect (a real node in dag.Nodes with no fqcn/block/
// parallel of its own) through the public Builder API.
func TestTaskKind_Classification(t *testing.T) {
	tests := []struct {
		name string
		task engine.Task
		want engine.TaskKind
	}{
		{"leaf", engine.Task{FQCN: "noop"}, engine.TaskKindLeaf},
		{"block", engine.Task{Block: []engine.Task{{FQCN: "noop"}}}, engine.TaskKindBlock},
		{"parallel", engine.Task{Parallel: []engine.Task{{FQCN: "noop"}}}, engine.TaskKindParallel},
		{"neither", engine.Task{}, engine.TaskKindInvalid},
		{"fqcn and block", engine.Task{FQCN: "noop", Block: []engine.Task{{FQCN: "noop"}}}, engine.TaskKindInvalid},
		{"fqcn and parallel", engine.Task{FQCN: "noop", Parallel: []engine.Task{{FQCN: "noop"}}}, engine.TaskKindInvalid},
		{"block and parallel", engine.Task{Block: []engine.Task{{FQCN: "noop"}}, Parallel: []engine.Task{{FQCN: "noop"}}}, engine.TaskKindInvalid},
		{"all three", engine.Task{FQCN: "noop", Block: []engine.Task{{FQCN: "noop"}}, Parallel: []engine.Task{{FQCN: "noop"}}}, engine.TaskKindInvalid},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.task.Kind(); got != tt.want {
				t.Errorf("Kind() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTaskKind_String confirms every declared TaskKind value, plus an
// out-of-range one, prints a stable, non-empty name for error messages and
// logs.
func TestTaskKind_String(t *testing.T) {
	tests := []struct {
		kind engine.TaskKind
		want string
	}{
		{engine.TaskKindLeaf, "leaf"},
		{engine.TaskKindBlock, "block"},
		{engine.TaskKindParallel, "parallel"},
		{engine.TaskKindSynthetic, "synthetic"},
		{engine.TaskKindInvalid, "invalid"},
		{engine.TaskKind(99), "invalid"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("TaskKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

// TestEdgeType_String confirms every declared EdgeType value, plus an
// out-of-range one, prints a stable, non-empty name.
func TestEdgeType_String(t *testing.T) {
	tests := []struct {
		edge engine.EdgeType
		want string
	}{
		{engine.EdgeTypeOnSuccess, "on_success"},
		{engine.EdgeTypeOnFailure, "on_failure"},
		{engine.EdgeTypeAlways, "always"},
		{engine.EdgeType(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.edge.String(); got != tt.want {
			t.Errorf("EdgeType(%d).String() = %q, want %q", tt.edge, got, tt.want)
		}
	}
}

// TestEdgeConfig_ZeroValueIsOnSuccess confirms a bare EdgeConfig{To: ...}
// (every edge synthesizeChain/synthesizeParallel produce today) defaults
// to EdgeTypeOnSuccess, so adding EdgeType changed no existing runbook's
// behavior.
func TestEdgeConfig_ZeroValueIsOnSuccess(t *testing.T) {
	edge := engine.EdgeConfig{To: "next"}
	if edge.Type != engine.EdgeTypeOnSuccess {
		t.Errorf("expected a bare EdgeConfig's Type to default to EdgeTypeOnSuccess, got %v", edge.Type)
	}
}
