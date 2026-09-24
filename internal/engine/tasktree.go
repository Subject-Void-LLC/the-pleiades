package engine

import "fmt"

// taskLabel formats a task's synthesized ID and, if present, its Name for
// use in an error message, e.g. `tasks[2] (name "reboot device")`. Name is
// a free-form human label with no uniqueness requirement, so it is
// included only to help a human locate the offending task, never as part
// of the ID itself.
func taskLabel(id string, task *Task) string {
	if task.Name == "" {
		return id
	}
	return fmt.Sprintf("%s (name %q)", id, task.Name)
}

// registerTask validates task, compiles its embedded Conditional and
// records task in dag.Nodes (and, if conditional, dag.Conditions), both
// keyed by id. It is the builder's register step for the shared walk
// (chainWalker, chain.go), used for the happy-path chain and the
// rescue/always coverage walk alike.
func (b *Builder) registerTask(dag *DAG, task *Task, id string) error {
	if err := validateTask(task, id); err != nil {
		return err
	}

	// Compile this task's optional when/when_or/when_cel skip condition.
	// An unconditional task yields a nil Program and no error, so it gets
	// no entry in dag.Conditions.
	prg, err := task.Conditional.Compile(b.cel)
	if err != nil {
		return fmt.Errorf("failed to compile when-condition for task %s: %w", taskLabel(id, task), err)
	}
	if prg != nil {
		dag.Conditions[id] = prg
	}

	dag.Nodes[id] = task
	return nil
}
