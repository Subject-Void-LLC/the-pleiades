package engine_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// TestLevelIterator_LinearChain confirms that over the only shape the
// current Builder ever produces (a linked list), every level is exactly
// one node, in the same order TopologicalOrder already reports.
func TestLevelIterator_LinearChain(t *testing.T) {
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

	it := engine.NewLevelIterator(dag)
	var got [][]string
	for {
		level, ok := it.Next()
		if !ok {
			break
		}
		got = append(got, level)
	}
	if err := it.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := [][]string{{"tasks[0]"}, {"tasks[1]"}, {"tasks[2]"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected levels %v, got %v", want, got)
	}
}

// TestLevelIterator_EmptyDAG confirms a DAG with no EntryPoint (every one
// of PreTasks, Tasks, and PostTasks is empty) yields no levels at all,
// mirroring TopologicalOrder's own (nil, nil) result for the same input.
func TestLevelIterator_EmptyDAG(t *testing.T) {
	dag := &engine.DAG{}

	it := engine.NewLevelIterator(dag)
	level, ok := it.Next()
	if ok || level != nil {
		t.Fatalf("expected no levels for an empty DAG, got %v, %v", level, ok)
	}
	if it.Err() != nil {
		t.Fatalf("unexpected error: %v", it.Err())
	}
}

// TestLevelIterator_Diamond builds a DAG directly (bypassing Builder,
// which cannot produce anything but a linked list) shaped like a diamond:
// A feeds both B and C, and both B and C feed D. This is exactly the
// graph shape PATTERNS.md's Composite entry says the DAG type must
// support ("nodes can have multiple parents"), so LevelIterator must
// group B and C into the same level (both become ready as soon as A
// finishes) and hold D back until both are done.
func TestLevelIterator_Diamond(t *testing.T) {
	dag := &engine.DAG{
		EntryPoint: "A",
		Nodes: map[string]*engine.Task{
			"A": {Name: "A", FQCN: "noop"},
			"B": {Name: "B", FQCN: "noop"},
			"C": {Name: "C", FQCN: "noop"},
			"D": {Name: "D", FQCN: "noop"},
		},
		Adjacency: map[string][]engine.EdgeConfig{
			"A": {{To: "B"}, {To: "C"}},
			"B": {{To: "D"}},
			"C": {{To: "D"}},
		},
	}

	it := engine.NewLevelIterator(dag)

	level1, ok := it.Next()
	if !ok || !reflect.DeepEqual(level1, []string{"A"}) {
		t.Fatalf("expected level 1 to be [A], got %v, %v", level1, ok)
	}

	level2, ok := it.Next()
	if !ok {
		t.Fatalf("expected a second level")
	}
	sort.Strings(level2)
	if !reflect.DeepEqual(level2, []string{"B", "C"}) {
		t.Fatalf("expected level 2 to be [B C] (concurrent), got %v", level2)
	}

	level3, ok := it.Next()
	if !ok || !reflect.DeepEqual(level3, []string{"D"}) {
		t.Fatalf("expected level 3 to be [D], got %v, %v", level3, ok)
	}

	level4, ok := it.Next()
	if ok || level4 != nil {
		t.Fatalf("expected no fourth level, got %v, %v", level4, ok)
	}
	if it.Err() != nil {
		t.Fatalf("unexpected error: %v", it.Err())
	}
}

// TestLevelIterator_ExcludesRescueAlwaysAndBlockParent confirms
// LevelIterator honors the exact same reachability rule TopologicalOrder
// already does: Rescue, Always, and a block task's own superseded ID
// never appear as a source or target in Adjacency, so they never appear
// in any level either, even though they still exist in dag.Nodes.
func TestLevelIterator_ExcludesRescueAlwaysAndBlockParent(t *testing.T) {
	eval, _ := engine.NewCELEvaluator()
	builder := engine.NewBuilder(eval)

	payload := []byte(`{
		"id": "runbook-block",
		"tasks": [
			{"name": "before", "fqcn": "noop"},
			{"name": "risky", "block": [
				{"name": "b0", "fqcn": "noop"}
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

	it := engine.NewLevelIterator(dag)
	var seen []string
	for {
		level, ok := it.Next()
		if !ok {
			break
		}
		seen = append(seen, level...)
	}
	if err := it.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"tasks[0]", "tasks[1].block[0]", "tasks[2]"}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("expected levels to cover exactly %v, got %v", want, seen)
	}
}

// TestLevelIterator_Deterministic confirms two iterators over the
// identical DAG produce identical levels, matching TopologicalOrder's own
// determinism guarantee: a "plan" a user diffs between two runs should
// not shuffle for no reason.
func TestLevelIterator_Deterministic(t *testing.T) {
	dag := &engine.DAG{
		EntryPoint: "A",
		Nodes: map[string]*engine.Task{
			"A": {Name: "A", FQCN: "noop"},
			"B": {Name: "B", FQCN: "noop"},
			"C": {Name: "C", FQCN: "noop"},
		},
		Adjacency: map[string][]engine.EdgeConfig{
			"A": {{To: "B"}, {To: "C"}},
		},
	}

	collect := func() [][]string {
		it := engine.NewLevelIterator(dag)
		var levels [][]string
		for {
			level, ok := it.Next()
			if !ok {
				break
			}
			levels = append(levels, level)
		}
		return levels
	}

	first := collect()
	second := collect()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("expected identical levels across runs, got %v then %v", first, second)
	}
}
