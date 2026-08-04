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

// validateTask checks the structural rules a Task must satisfy regardless
// of where it appears in the tree: exactly one of FQCN (a leaf task) or
// Block (a block task) must be set, having both or neither is an error,
// mirroring Ansible where a task is either a module call or a block. It
// also rejects Rescue or Always set without a Block, since rescue/always
// without a block to guard is meaningless: there is nothing that could
// fail. Every error names the task's synthesized ID (and Name, if set) so
// it can be found in a large runbook.
func validateTask(task *Task, id string) error {
	hasFQCN := task.FQCN != ""
	hasBlock := len(task.Block) > 0

	switch {
	case hasFQCN && hasBlock:
		return fmt.Errorf("task %s sets both fqcn and block: a task must be exactly one of a module call (fqcn) or a block, never both", taskLabel(id, task))
	case !hasFQCN && !hasBlock:
		return fmt.Errorf("task %s has neither fqcn nor block: a task must be exactly one of a module call (fqcn) or a block", taskLabel(id, task))
	}

	if !hasBlock && (len(task.Rescue) > 0 || len(task.Always) > 0) {
		return fmt.Errorf("task %s sets rescue or always without a block: rescue/always only make sense guarding a block", taskLabel(id, task))
	}

	if task.SecretMask != nil {
		if task.SecretMask.Register == "" {
			return fmt.Errorf("task %s sets secret_mask with an empty register: it must name the earlier task's register to mask fields of", taskLabel(id, task))
		}
		if len(task.SecretMask.Fields) == 0 {
			return fmt.Errorf("task %s sets secret_mask with no fields: it must name at least one field to mask", taskLabel(id, task))
		}
	}

	return nil
}

// registerTask compiles task's embedded Conditional and records task in
// dag.Nodes (and, if conditional, dag.Conditions), both keyed by id. It is
// shared by synthesizeChain (the happy-path walk) and collectSubtree (the
// Rescue/Always coverage-only walk), since both need identical
// validate-compile-register handling for every task they visit.
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

// synthesizeChain walks tasks, an ordered list appearing at prefix (e.g.
// "tasks" for the top-level list, or "tasks[2].block" for a block task's
// children), assigning each task the synthesized ID "prefix[i]", and wires
// dag.Adjacency into a single happy-path chain across them. A block task
// does not appear in the chain itself: its Block children are recursively
// synthesized and their own chain (from their first entry to their last
// exit) splices into the position the block task occupies. Rescue and
// Always are walked separately via collectSubtree, for Nodes/Conditions
// coverage only, never joining the chain.
//
// It returns the entry (first) and exit (last) node ID reached by this
// list, so the caller can splice this list's chain into its own
// surrounding one. An empty tasks list returns ("", "").
func (b *Builder) synthesizeChain(dag *DAG, tasks []Task, prefix string) (entry, exit string, err error) {
	for i := range tasks {
		task := &tasks[i]
		id := fmt.Sprintf("%s[%d]", prefix, i)

		if err := b.registerTask(dag, task, id); err != nil {
			return "", "", err
		}

		// taskEntry/taskExit are the node(s) this task actually
		// contributes to the happy-path chain: a leaf task contributes
		// itself; a block task contributes its Block children's chain
		// instead, splicing into the position it occupies.
		taskEntry, taskExit := id, id
		if len(task.Block) > 0 {
			blockEntry, blockExit, err := b.synthesizeChain(dag, task.Block, id+".block")
			if err != nil {
				return "", "", err
			}
			taskEntry, taskExit = blockEntry, blockExit
		}

		if len(task.Rescue) > 0 {
			if err := b.collectSubtree(dag, task.Rescue, id+".rescue"); err != nil {
				return "", "", err
			}
		}
		if len(task.Always) > 0 {
			if err := b.collectSubtree(dag, task.Always, id+".always"); err != nil {
				return "", "", err
			}
		}

		if entry == "" {
			// First task in this list: it is this list's entry point.
			entry = taskEntry
		}
		if exit != "" {
			// Chain the previous task's exit into this task's entry.
			dag.Adjacency[exit] = append(dag.Adjacency[exit], EdgeConfig{To: taskEntry})
		}
		exit = taskExit
	}
	return entry, exit, nil
}

// collectSubtree walks tasks (a Rescue or Always list, at prefix) purely
// for Nodes/Conditions coverage: it validates and registers every task, and
// recurses into any further Block/Rescue/Always nesting, but never touches
// dag.Adjacency. This mirrors why Rescue and Always are excluded from the
// happy-path chain (see DAG.Adjacency's doc comment): once inside a
// Rescue/Always subtree, none of its descendants, at any depth, join the
// chain either.
func (b *Builder) collectSubtree(dag *DAG, tasks []Task, prefix string) error {
	for i := range tasks {
		task := &tasks[i]
		id := fmt.Sprintf("%s[%d]", prefix, i)

		if err := b.registerTask(dag, task, id); err != nil {
			return err
		}

		if len(task.Block) > 0 {
			if err := b.collectSubtree(dag, task.Block, id+".block"); err != nil {
				return err
			}
		}
		if len(task.Rescue) > 0 {
			if err := b.collectSubtree(dag, task.Rescue, id+".rescue"); err != nil {
				return err
			}
		}
		if len(task.Always) > 0 {
			if err := b.collectSubtree(dag, task.Always, id+".always"); err != nil {
				return err
			}
		}
	}
	return nil
}
