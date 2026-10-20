package engine_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestTopologicalOrder_LinearTasks confirms a simple tasks: list, with no
// pretasks or posttasks, orders exactly as authored.
func TestTopologicalOrder_LinearTasks(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-linear",
		"tasks": [
			{"name": "start", "noop": {}},
			{"name": "ping", "ssh_exec": {}},
			{"name": "reboot", "ios_backup": {}}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	order, err := engine.TopologicalOrder(dag)
	if err != nil {
		t.Fatalf("TopologicalOrder returned an error: %v", err)
	}

	want := []string{"tasks[0]", "tasks[1]", "tasks[2]"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}
}

// TestTopologicalOrder_PreTasksAndPostTasksStitchIn confirms pretasks and
// posttasks join the same chain as tasks, in section order.
func TestTopologicalOrder_PreTasksAndPostTasksStitchIn(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-sections",
		"pretasks": [{"name": "pre0", "noop": {}}],
		"tasks": [{"name": "t0", "noop": {}}],
		"posttasks": [{"name": "post0", "noop": {}}]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	order, err := engine.TopologicalOrder(dag)
	if err != nil {
		t.Fatalf("TopologicalOrder returned an error: %v", err)
	}

	want := []string{"pretasks[0]", "tasks[0]", "posttasks[0]"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}
}

// TestTopologicalOrder_ExcludesTheBlockParent reproduces the exact
// regression this test guards against: a chain tasks[0] ->
// {block/rescue/always} -> tasks[2] used to return tasks[1] (the block
// task's own now-superseded ID) as a disconnected "island" interleaved into
// the order, because the prior implementation seeded Kahn's algorithm from
// every key in dag.Nodes (the fully-flattened map) instead of only nodes
// reachable from dag.EntryPoint via dag.Adjacency. The block's own ID must
// still land in dag.Nodes (for validation/capability-checking coverage) and
// never appear in the order; its rescue and always tasks come after its own
// tasks, in that order, since every device walks them and each decides
// whether they run there (handlers.go).
func TestTopologicalOrder_ExcludesTheBlockParent(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-block",
		"tasks": [
			{"name": "before", "noop": {}},
			{"name": "risky", "block": [
				{"name": "b0", "ssh_exec": {}}
			], "rescue": [
				{"name": "r0", "noop": {}}
			], "always": [
				{"name": "a0", "noop": {}}
			]},
			{"name": "after", "noop": {}}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}
	if _, ok := dag.Nodes["tasks[1]"]; !ok {
		t.Fatal("expected the block's own node to exist in dag.Nodes")
	}

	order, err := engine.TopologicalOrder(dag)
	if err != nil {
		t.Fatalf("TopologicalOrder returned an error: %v", err)
	}
	want := []string{"tasks[0]", "tasks[1].block[0]", "tasks[1].rescue[0]", "tasks[1].always[0]", "tasks[2]"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}
}

// TestTopologicalOrder_EmptyDAG confirms a DAG with no pretasks, tasks, or
// posttasks at all (EntryPoint == "") returns a nil order and no error,
// rather than panicking or erroring.
func TestTopologicalOrder_EmptyDAG(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-empty",
		"tasks": []
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	order, err := engine.TopologicalOrder(dag)
	if err != nil {
		t.Fatalf("TopologicalOrder returned an error: %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("expected an empty order for a DAG with no tasks, got %v", order)
	}
}
