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
			{"name": "start", "fqcn": "noop"},
			{"name": "ping", "fqcn": "ssh_exec"},
			{"name": "reboot", "fqcn": "ios_backup"}
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
		"pretasks": [{"name": "pre0", "fqcn": "noop"}],
		"tasks": [{"name": "t0", "fqcn": "noop"}],
		"posttasks": [{"name": "post0", "fqcn": "noop"}]
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

// TestTopologicalOrder_ExcludesRescueAlwaysAndBlockParent reproduces the
// exact regression this test guards against: a chain tasks[0] ->
// {block/rescue/always} -> tasks[2] used to return tasks[1] (the block
// task's own now-superseded ID) and its rescue/always children as
// disconnected "islands" interleaved into the order, because the prior
// implementation seeded Kahn's algorithm from every key in dag.Nodes (the
// fully-flattened map) instead of only nodes reachable from dag.EntryPoint
// via dag.Adjacency. Rescue, Always, and the block task's own ID must still
// land in dag.Nodes (for validation/capability-checking coverage), but must
// never appear in the returned order.
func TestTopologicalOrder_ExcludesRescueAlwaysAndBlockParent(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-block",
		"tasks": [
			{"name": "before", "fqcn": "noop"},
			{"name": "risky", "block": [
				{"name": "b0", "fqcn": "ssh_exec"}
			], "rescue": [
				{"name": "r0", "fqcn": "noop"}
			], "always": [
				{"name": "a0", "fqcn": "noop"}
			]},
			{"name": "after", "fqcn": "noop"}
		]
	}`)

	dag, err := builder.Build(payload)
	if err != nil {
		t.Fatalf("failed to build valid DAG: %v", err)
	}

	// Sanity check: Rescue, Always, and the block task's own ID are still
	// covered in Nodes, exactly as TestDAGBuilder_BlockRescueAlways
	// confirms for the builder itself.
	for _, id := range []string{"tasks[1]", "tasks[1].rescue[0]", "tasks[1].always[0]"} {
		if _, ok := dag.Nodes[id]; !ok {
			t.Fatalf("expected node %q to exist in dag.Nodes", id)
		}
	}

	order, err := engine.TopologicalOrder(dag)
	if err != nil {
		t.Fatalf("TopologicalOrder returned an error: %v", err)
	}

	want := []string{"tasks[0]", "tasks[1].block[0]", "tasks[2]"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}

	for _, id := range []string{"tasks[1]", "tasks[1].rescue[0]", "tasks[1].always[0]"} {
		for _, got := range order {
			if got == id {
				t.Errorf("expected %q to be excluded from the order, but it appeared in %v", id, order)
			}
		}
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
