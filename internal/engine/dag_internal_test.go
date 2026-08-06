package engine

import "testing"

// TestHasCycle_DetectsSimpleCycle proves the iterative rewrite (Phase 10)
// still detects a cycle correctly, the one property the external test
// package cannot exercise itself: Builder never produces a cyclic
// Adjacency graph (dag_test.go's own note on this), so this hand-built,
// package-internal case is the only way to prove hasCycle's own
// correctness rather than only its stack safety.
func TestHasCycle_DetectsSimpleCycle(t *testing.T) {
	dag := &DAG{
		Nodes: map[string]*Task{
			"a": {FQCN: "noop"},
			"b": {FQCN: "noop"},
			"c": {FQCN: "noop"},
		},
		Adjacency: map[string][]EdgeConfig{
			"a": {{To: "b"}},
			"b": {{To: "c"}},
			"c": {{To: "a"}}, // back-edge to a, closing the cycle
		},
	}

	if !hasCycle(dag) {
		t.Fatal("expected hasCycle to detect the a -> b -> c -> a cycle")
	}
}

// TestHasCycle_NoFalsePositiveOnDiamond proves a diamond (the same shape
// TestLevelIterator_Diamond, level_iterator_test.go, uses for a parallel
// fan-out/join) is not mistaken for a cycle: a node reached twice via two
// different paths (B and C both feeding D) is a legitimate multi-parent
// DAG shape, not a back-edge.
func TestHasCycle_NoFalsePositiveOnDiamond(t *testing.T) {
	dag := &DAG{
		Nodes: map[string]*Task{
			"a": {FQCN: "noop"},
			"b": {FQCN: "noop"},
			"c": {FQCN: "noop"},
			"d": {FQCN: "noop"},
		},
		Adjacency: map[string][]EdgeConfig{
			"a": {{To: "b"}, {To: "c"}},
			"b": {{To: "d"}},
			"c": {{To: "d"}},
		},
	}

	if hasCycle(dag) {
		t.Fatal("expected hasCycle to report no cycle for a diamond-shaped DAG")
	}
}

// TestHasCycle_NoStackOverflowOnLongChain is this phase's real Schema/
// Injection Hardening proof: a long flat chain (no nesting at all, so
// maxTaskNestingDepth, import_tasks.go, does not bound it) drove the old
// recursive hasCycle's stack usage directly with input size. 500,000 nodes
// is comfortably beyond what the old recursive implementation could
// traverse without materially growing its goroutine stack; the iterative
// rewrite must return promptly and correctly (no cycle) rather than crash
// or hang.
func TestHasCycle_NoStackOverflowOnLongChain(t *testing.T) {
	const n = 500_000

	nodes := make(map[string]*Task, n)
	adjacency := make(map[string][]EdgeConfig, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := itoa(i)
		ids[i] = id
		nodes[id] = &Task{FQCN: "noop"}
	}
	for i := 0; i < n-1; i++ {
		adjacency[ids[i]] = []EdgeConfig{{To: ids[i+1]}}
	}

	dag := &DAG{Nodes: nodes, Adjacency: adjacency}

	if hasCycle(dag) {
		t.Fatal("expected no cycle on a long acyclic chain")
	}
}

// itoa is a tiny, allocation-light int-to-string helper for
// TestHasCycle_NoStackOverflowOnLongChain's synthesized node IDs, avoiding
// an fmt.Sprintf per node across 500,000 iterations.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
