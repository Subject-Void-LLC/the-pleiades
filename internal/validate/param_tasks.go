// Package validate: the tasks whose parameters render, a runbook's own and
// the rollback: steps beside them.
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// paramTask is one call whose params a rule reads: a node of the DAG, or
// one of a node's rollback: steps, which render when the engine undoes the
// node inside the run (rollback_on_failure) or `pleiades rollback` replays
// them later.
type paramTask struct {
	// id names it in a finding: the node's id, or "<node>.rollback[<i>]"
	// for a step, as RollbackRule names one.
	id string
	// node is the DAG node it belongs to: its own id, or the id of the task
	// whose rollback: list holds it. A step runs only where its task ran, so
	// its task's condition is its own.
	node string
	// task is the call itself.
	task *engine.Task
}

// paramTasks returns every node of dag and each of its rollback: steps,
// sorted by id, so findings come out in a stable order.
func paramTasks(dag *engine.DAG) []paramTask {
	var out []paramTask
	for id, task := range dag.Nodes {
		out = append(out, paramTask{id: id, node: id, task: task})
		for i := range task.Rollback {
			out = append(out, paramTask{id: fmt.Sprintf("%s.rollback[%d]", id, i), node: id, task: &task.Rollback[i]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
