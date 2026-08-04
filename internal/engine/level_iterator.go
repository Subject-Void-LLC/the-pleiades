package engine

import (
	"fmt"
	"sort"
)

// LevelIterator walks a DAG's happy-path chain (see DAG.Adjacency) one
// topological level at a time, instead of one node at a time the way
// TopologicalOrder's flat order does. Every node returned in one level has
// every dependency already returned by an earlier call to Next, so a
// caller is free to execute everything within one level concurrently, and
// everything in the next level can safely assume every earlier level has
// already finished. It mirrors inventory.Iterator's Next/Error shape
// (PATTERNS.md's Iterator entry), the codebase's existing cursor
// convention, rather than inventing a new one.
//
// The Adjacency graph this walks always has out-degree 1 and in-degree 1
// today, since synthesizeChain (dag.go) only ever produces a linked list;
// every level this iterator returns is therefore exactly one node. Nothing
// about LevelIterator assumes that shape, on purpose: PATTERNS.md's
// Composite entry explicitly rejects modeling the DAG as a tree ("nodes
// can have multiple parents"), and a later phase adding parallel task
// groups or Register-inferred edges (HANDOFF_DOCUMENT.md's "highest-value
// change available" note) would produce a real multi-node level without
// this type changing at all. Executor (executor.go) is what actually
// exercises that generality, running every node in a level concurrently.
type LevelIterator struct {
	dag       *DAG
	inDegree  map[string]int
	remaining map[string]bool // reachable nodes not yet returned by Next
	err       error
}

// NewLevelIterator prepares a LevelIterator over dag's happy-path chain,
// starting at dag.EntryPoint exactly as TopologicalOrder does.
func NewLevelIterator(dag *DAG) *LevelIterator {
	reachable, inDegree := reachableWithInDegree(dag)

	remaining := make(map[string]bool, len(reachable))
	for id := range reachable {
		remaining[id] = true
	}

	return &LevelIterator{dag: dag, inDegree: inDegree, remaining: remaining}
}

// Next returns the next level, the set of not-yet-returned node IDs whose
// every dependency has already been returned by an earlier call, and
// true. It returns (nil, false) once every reachable node has been
// returned, or once progress stalls (see Err). A level's own node IDs are
// sorted, matching TopologicalOrder's existing determinism guarantee:
// identical input always yields identical levels.
func (it *LevelIterator) Next() ([]string, bool) {
	if it.err != nil || len(it.remaining) == 0 {
		return nil, false
	}

	var level []string
	for id := range it.remaining {
		if it.inDegree[id] == 0 {
			level = append(level, id)
		}
	}

	if len(level) == 0 {
		// Every remaining node has a positive in-degree with nothing left
		// to reduce it further. Build already rejects cycles, so this
		// should be unreachable; guard it anyway rather than loop forever
		// or silently return a partial walk (mirrors TopologicalOrder's
		// own defensive framing for the identical situation).
		it.err = fmt.Errorf("topological levels stalled with %d node(s) remaining: dag has a cycle Build should have rejected", len(it.remaining))
		return nil, false
	}

	sort.Strings(level)
	for _, id := range level {
		delete(it.remaining, id)
		for _, edge := range it.dag.Adjacency[id] {
			it.inDegree[edge.To]--
		}
	}

	return level, true
}

// Err returns any error Next encountered. It is nil unless Next returned
// (nil, false) because progress stalled, which Build's own cycle
// rejection should make unreachable in practice.
func (it *LevelIterator) Err() error {
	return it.err
}
