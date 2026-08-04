package engine

import (
	"fmt"
	"sort"
)

// TopologicalOrder returns one valid execution order for dag's happy-path
// chain, using Kahn's algorithm. It is a plain graph ordering utility: it
// walks dag.Adjacency starting from dag.EntryPoint, the synthesized
// happy-path chain (pretasks, then tasks, then posttasks, with block
// children spliced in), and does not evaluate any node's compiled
// when/when_or/when_cel condition (DAG.Conditions) or execute anything.
// Only nodes reachable from EntryPoint via Adjacency are included: Rescue,
// Always, and a block task's own superseded ID never appear as a source or
// target in Adjacency (see DAG.Adjacency), so they are excluded from the
// returned order even though they still appear in dag.Nodes and
// dag.Conditions for validation and capability-checking. `pleiades run`'s
// plan-printing uses this flat order; Executor (executor.go) uses
// LevelIterator (level_iterator.go) instead, since it needs to know which
// nodes may run concurrently, not just one valid serial order. Both build
// on reachableWithInDegree so "what counts as in the happy-path chain"
// lives in exactly one place (Gate 1 reusability).
func TopologicalOrder(dag *DAG) ([]string, error) {
	if dag.EntryPoint == "" {
		// PreTasks, Tasks, and PostTasks are all empty: no chain exists.
		return nil, nil
	}

	reachable, inDegree := reachableWithInDegree(dag)

	// Seed the queue with every node that has no incoming edge, sorted so
	// identical input always produces the identical order: a "plan" a
	// user diffs between two runs should not shuffle for no reason.
	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	order := make([]string, 0, len(reachable))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)

		var freed []string
		for _, edge := range dag.Adjacency[id] {
			inDegree[edge.To]--
			if inDegree[edge.To] == 0 {
				freed = append(freed, edge.To)
			}
		}
		sort.Strings(freed)
		queue = append(queue, freed...)
	}

	if len(order) != len(reachable) {
		// Build already rejects cycles, so this should be unreachable.
		// Guard it anyway rather than silently return a partial order.
		return nil, fmt.Errorf("topological sort covered %d of %d reachable nodes: dag has a cycle Build should have rejected", len(order), len(reachable))
	}

	return order, nil
}

// reachableWithInDegree computes every node ID reachable from
// dag.EntryPoint via Adjacency (a breadth-first walk of the happy-path
// chain), plus each reachable node's in-degree, counted only from edges
// between two reachable nodes. TopologicalOrder and LevelIterator both
// start from this exact result, so a node's reachability and its
// in-degree can never quietly diverge between the two.
func reachableWithInDegree(dag *DAG) (reachable map[string]bool, inDegree map[string]int) {
	reachable = map[string]bool{}
	if dag.EntryPoint == "" {
		return reachable, map[string]int{}
	}

	reachable[dag.EntryPoint] = true
	toVisit := []string{dag.EntryPoint}
	for len(toVisit) > 0 {
		id := toVisit[0]
		toVisit = toVisit[1:]
		for _, edge := range dag.Adjacency[id] {
			if !reachable[edge.To] {
				reachable[edge.To] = true
				toVisit = append(toVisit, edge.To)
			}
		}
	}

	inDegree = make(map[string]int, len(reachable))
	for id := range reachable {
		inDegree[id] = 0
	}
	for id := range reachable {
		for _, edge := range dag.Adjacency[id] {
			inDegree[edge.To]++
		}
	}

	return reachable, inDegree
}
