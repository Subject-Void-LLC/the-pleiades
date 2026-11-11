// Package runbook: what each node of a compiled runbook runs, as the record
// the Controller keeps of the graph a job was prepared from.
//
// The Controller's facts and journal consumers resolve the method a Runner's
// batch names for a node from this record, pinned on the job at its first
// fan-out, and never from the runbook as it is later: a runbook edited while
// a job runs would otherwise answer differently for the same batch.
package runbook

import "github.com/Subject-Void-LLC/the-pleiades/internal/engine"

// Node is what one node of a compiled runbook runs.
type Node struct {
	// Method is the task's FQCN as the runbook names it; empty for the
	// parallel marker.
	Method string `json:"method,omitempty"`
	// Marker is set for the parallel fan-out and join marker the builder
	// adds, which runs no method.
	Marker bool `json:"marker,omitempty"`
	// Authored is set when the task has a rollback: list, empty or not: its
	// undo is then the list, never the undo its method records.
	Authored bool `json:"authored,omitempty"`
	// Rollback is each step of the task's rollback: list, its FQCN, in
	// order.
	Rollback []string `json:"rollback,omitempty"`
}

// NodesOf returns, by node id, what each node of dag that runs a method, or
// marks a parallel group, runs. A block or a parallel group's own node runs
// nothing and is left out.
func NodesOf(dag *engine.DAG) map[string]Node {
	out := make(map[string]Node, len(dag.Nodes))
	for id, task := range dag.Nodes {
		if task == nil {
			continue
		}
		switch task.Kind() {
		case engine.TaskKindSynthetic:
			out[id] = Node{Marker: true}
		case engine.TaskKindLeaf:
			n := Node{Method: task.FQCN, Authored: task.Rollback != nil}
			for _, step := range task.Rollback {
				n.Rollback = append(n.Rollback, step.FQCN)
			}
			out[id] = n
		}
	}
	return out
}
