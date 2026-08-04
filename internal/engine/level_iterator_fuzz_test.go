package engine_test

import (
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// synthesizeAcyclicDAG deterministically builds a *engine.DAG from data,
// for FuzzLevelIterator. Node i (for i >= 1) always gets an edge from some
// node with a strictly lower index, guaranteeing every node is reachable
// from "n0" and that the graph is acyclic by construction (no edge ever
// points backward), without needing dag.go's own tree-walk builder, which
// cannot produce anything but a linked list. A second, independent parent
// edge is added for roughly one in three nodes, which is what produces a
// genuine diamond (two parents converging on one child) rather than only
// ever a tree of chains and fan-outs.
func synthesizeAcyclicDAG(data []byte) *engine.DAG {
	numNodes := 1
	if len(data) > 0 {
		numNodes = 1 + int(data[0])%16
	}

	nodeName := func(i int) string { return fmt.Sprintf("n%d", i) }

	dag := &engine.DAG{
		EntryPoint: nodeName(0),
		Nodes:      make(map[string]*engine.Task, numNodes),
		Adjacency:  make(map[string][]engine.EdgeConfig),
	}
	for i := 0; i < numNodes; i++ {
		dag.Nodes[nodeName(i)] = &engine.Task{Name: nodeName(i), FQCN: "noop"}
	}

	pick := func(offset, modulus int) int {
		if len(data) == 0 || modulus <= 0 {
			return 0
		}
		return int(data[offset%len(data)]) % modulus
	}

	for i := 1; i < numNodes; i++ {
		parent1 := nodeName(pick(i, i))
		dag.Adjacency[parent1] = append(dag.Adjacency[parent1], engine.EdgeConfig{To: nodeName(i)})

		if pick(i*7+1, 3) == 0 && i >= 2 {
			parent2 := nodeName(pick(i*13+2, i))
			if parent2 != parent1 {
				dag.Adjacency[parent2] = append(dag.Adjacency[parent2], engine.EdgeConfig{To: nodeName(i)})
			}
		}
	}

	return dag
}

// FuzzLevelIterator proves LevelIterator never panics, never returns a
// node twice, and never returns a node before every node with an edge
// into it, across arbitrary acyclic graph shapes, including diamonds
// (a node with two parents) and long chains (Phase W5's own Fuzz/Stress
// Test requirement). synthesizeAcyclicDAG guarantees the input graph is
// acyclic by construction, so LevelIterator.Err() must stay nil; a
// non-nil Err on this fuzz corpus is itself a bug, not an expected
// rejection.
func FuzzLevelIterator(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{5, 0, 0, 0, 0})                // a long chain: every node's only pick is 0
	f.Add([]byte{4, 0, 0, 1, 3, 0, 1, 2, 3, 4}) // likely to produce at least one diamond
	f.Add([]byte{16, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})

	f.Fuzz(func(t *testing.T, data []byte) {
		dag := synthesizeAcyclicDAG(data)

		it := engine.NewLevelIterator(dag)
		seenAtLevel := make(map[string]int)
		levelIdx := 0
		for {
			level, ok := it.Next()
			if !ok {
				break
			}
			for _, id := range level {
				if _, dup := seenAtLevel[id]; dup {
					t.Fatalf("node %q returned in more than one level", id)
				}
				seenAtLevel[id] = levelIdx
			}
			levelIdx++
		}

		if err := it.Err(); err != nil {
			t.Fatalf("synthesizeAcyclicDAG built an acyclic graph, but LevelIterator reported an error: %v", err)
		}

		if len(seenAtLevel) != len(dag.Nodes) {
			t.Fatalf("expected every one of %d nodes to appear in exactly one level, got %d", len(dag.Nodes), len(seenAtLevel))
		}

		for from, edges := range dag.Adjacency {
			for _, edge := range edges {
				if seenAtLevel[from] >= seenAtLevel[edge.To] {
					t.Fatalf("ordering violated: %s (level %d) has an edge to %s (level %d)", from, seenAtLevel[from], edge.To, seenAtLevel[edge.To])
				}
			}
		}
	})
}
