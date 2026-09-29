// Package engine: the structural rules every task in a runbook's tree
// must satisfy, checked as the builder registers it.
package engine

import "fmt"

// validateTask checks the structural rules a Task must satisfy regardless
// of where it appears in the tree: exactly one of FQCN (a leaf task),
// Block (a block task), or Parallel (a parallel task) must be set, having
// more than one or none of them is an error, mirroring Ansible where a
// task is either a module call or a block (Parallel is The Pleiades' own
// addition, PLAN.md Section 14). It also rejects Rescue or Always set
// without a Block, since rescue/always without a block to guard is
// meaningless: there is nothing that could fail, and a Parallel task
// carrying either is rejected the same way, deliberately - Ansible has no
// established parallel-failure-handling vocabulary to mirror. It also
// rejects when/when_or/when_cel or secret_mask set directly on a block or
// parallel task, since neither ever fires there (see the rejection's own
// comment below for why). Every error names the task's synthesized ID
// (and Name, if set) so it can be found in a large runbook.
func validateTask(task *Task, id string) error {
	if task.Kind() == TaskKindInvalid {
		hasFQCN, hasBlock, hasParallel := taskShape(task)
		var set []string
		if hasFQCN {
			set = append(set, "fqcn")
		}
		if hasBlock {
			set = append(set, "block")
		}
		if hasParallel {
			set = append(set, "parallel")
		}
		if len(set) == 0 {
			return fmt.Errorf("task %s has none of fqcn, block, or parallel: a task must be exactly one of a module call (fqcn), a block, or a parallel group", taskLabel(id, task))
		}
		return fmt.Errorf("task %s sets more than one of fqcn, block, and parallel (%v): a task must be exactly one of a module call (fqcn), a block, or a parallel group, never more than one", taskLabel(id, task), set)
	}

	if task.Kind() != TaskKindBlock && (len(task.Rescue) > 0 || len(task.Always) > 0) {
		return fmt.Errorf("task %s sets rescue or always without a block: rescue/always only make sense guarding a block", taskLabel(id, task))
	}

	// A block or parallel task's own id never enters dag.Adjacency:
	// synthesizeOne splices in its CHILDREN's entry/exit instead (see that
	// function's own doc comment), so the executor's walk never visits the
	// grouping task's id and never consults dag.Conditions or applies a
	// secret_mask for it. Before this check, a when/when_or/when_cel or
	// secret_mask written directly on a block or parallel task compiled
	// cleanly and validated cleanly, then silently never fired: a
	// device-mutating group ran even when its own guard said not to, and a
	// value marked secret reached published events unmasked. Real
	// block/parallel-level gating (evaluating the condition once and
	// propagating the skip to every child) is a larger executor change and
	// not this fix; this fix is the "type safety moves left" half, turning
	// the silent no-op into a write-time error naming exactly where the
	// condition or mask belongs instead.
	if kind := task.Kind(); kind == TaskKindBlock || kind == TaskKindParallel {
		group := "block"
		if kind == TaskKindParallel {
			group = "parallel"
		}
		if len(task.When) > 0 || len(task.WhenOr) > 0 || task.WhenCEL != "" {
			return fmt.Errorf("task %s sets when/when_or/when_cel directly on a %s task: this condition compiles but is never evaluated, since only a leaf task's own id is wired into the executable graph; move it onto each child task inside the %s instead", taskLabel(id, task), group, group)
		}
		if task.SecretMask != nil {
			return fmt.Errorf("task %s sets secret_mask directly on a %s task: this mask compiles but is never applied, since only a leaf task's own id is wired into the executable graph; move it onto the specific child task whose result it should mask", taskLabel(id, task), group)
		}
	}

	if err := validateTarget(task, id); err != nil {
		return err
	}

	if task.SecretMask != nil {
		if task.SecretMask.Register == "" {
			return fmt.Errorf("task %s sets secret_mask with an empty register: it must name the earlier task's register to mask fields of", taskLabel(id, task))
		}
		if len(task.SecretMask.Fields) == 0 {
			return fmt.Errorf("task %s sets secret_mask with no fields: it must name at least one field to mask", taskLabel(id, task))
		}
	}

	return validateRollback(task, id)
}
