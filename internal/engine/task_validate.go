// Package engine: the structural rules every task in a runbook's tree
// must satisfy, checked as the builder registers it.
package engine

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

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
	if task.Kind() != TaskKindBlock && task.RollbackOnFailure {
		return fmt.Errorf("task %s sets rollback_on_failure without a block: it undoes what a block's tasks completed when one of them fails, so it belongs on the block; a single task's own undo is its rollback: list", taskLabel(id, task))
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
		if task.Timeout != 0 {
			return fmt.Errorf("task %s sets timeout directly on a %s task: a timeout bounds one task on one device, so set it on each child task inside the %s instead", taskLabel(id, task), group, group)
		}
	}

	if err := validateIteration(task, id); err != nil {
		return err
	}

	if task.Timeout < 0 || task.Timeout > MaxTaskTimeout {
		return fmt.Errorf("task %s sets timeout %d: it is from 1 to %d seconds (a day), written as seconds or a duration such as 30m, or left out", taskLabel(id, task), task.Timeout, MaxTaskTimeout)
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

// loopVarReserved are the names a loop_var or index_var may not take: the
// roots a template or a condition already reads.
var loopVarReserved = map[string]bool{
	TemplateRootVars: true, TemplateRootNodes: true, TemplateRootResult: true, TemplateRootError: true,
	TemplateRootDevice: true, "stat": true, "loop": true,
}

// validateIteration holds a task's loop, until, retries and delay keys to
// what the executor can run: a leaf task only, one way to say the loop, a
// list or one expression, a loop_control that names a loop, an until that
// has a register to read, and bounded counts.
func validateIteration(task *Task, id string) error {
	label := taskLabel(id, task)
	leaf := task.Kind() == TaskKindLeaf
	if task.Loop != nil && task.WithItems != nil {
		return fmt.Errorf("task %s sets both loop and with_items: they are the same keyword, so write one", label)
	}
	if src := task.loopSource(); src != nil {
		if !leaf {
			return fmt.Errorf("task %s sets loop on a block or parallel group: a loop runs one method call once per item, so set it on each task inside instead", label)
		}
		switch v := src.(type) {
		case []any:
			if len(v) > MaxLoopItems {
				return fmt.Errorf("task %s has a loop of %d items, and a loop runs at most %d", label, len(v), MaxLoopItems)
			}
		case string:
			if !HasTemplate(v) {
				return fmt.Errorf("task %s writes its loop as text %q: a loop is a list, or one {{ }} expression that renders to a list", label, v)
			}
		default:
			return fmt.Errorf("task %s writes its loop as %s: a loop is a list, or one {{ }} expression that renders to a list", label, collectionKind(src))
		}
	}
	if (task.loopSource() != nil || len(task.Until) > 0) && len(task.Rollback) > 0 {
		return fmt.Errorf("task %s sets rollback: on a task that loops or retries: its method runs more than once, and an authored undo written once cannot say which run it undoes; leave rollback: off and each run's own recorded undo is replayed, newest first", label)
	}
	if task.LoopControl != nil {
		if task.loopSource() == nil {
			return fmt.Errorf("task %s sets loop_control without a loop", label)
		}
		for _, name := range []string{task.LoopControl.LoopVar, task.LoopControl.IndexVar} {
			if name == "" {
				continue
			}
			if err := CheckVarNames([]string{name}); err != nil {
				return fmt.Errorf("task %s loop_control: %w", label, err)
			}
			if loopVarReserved[name] {
				return fmt.Errorf("task %s loop_control names %q, which a template already reads as something else", label, name)
			}
		}
	}
	if isAssert(task.FQCN) {
		if err := validateAssert(task, id); err != nil {
			return err
		}
	}
	if isSetFact(task.FQCN) && task.Iterates() {
		if global, _ := task.Params[SetFactGlobal].(bool); global {
			return fmt.Errorf("task %s sets a global fact in a loop or a retry: each run would replace the last for every device, and devices that disagree would race; set the fact once after the loop, or drop global", label)
		}
	}
	if len(task.ChangedWhen) > 0 || len(task.FailedWhen) > 0 {
		if !leaf {
			return fmt.Errorf("task %s sets changed_when or failed_when on a block or parallel group: they classify one method call's result, so set them on the task inside instead", label)
		}
		if task.Register == "" {
			return fmt.Errorf("task %s sets changed_when or failed_when without a register: they read the task's own result, so name it with register", label)
		}
	}
	if task.IgnoreErrors && !leaf {
		return fmt.Errorf("task %s sets ignore_errors on a block or parallel group: it applies to one method call, so set it on the task inside (a block's failure is answered by rescue)", label)
	}
	if len(task.Until) > 0 {
		if !leaf {
			return fmt.Errorf("task %s sets until on a block or parallel group: it runs one method call again, so set it on the task inside instead", label)
		}
		if task.Register == "" {
			return fmt.Errorf("task %s sets until without a register: until reads the task's own result, so name it with register", label)
		}
	} else if task.Retries != nil || task.Delay != nil {
		return fmt.Errorf("task %s sets retries or delay without until: they only say how an until task is tried again", label)
	}
	if task.Retries != nil && (*task.Retries < 0 || *task.Retries > MaxRetries) {
		return fmt.Errorf("task %s sets retries %d: it is from 0 to %d", label, *task.Retries, MaxRetries)
	}
	if task.Delay != nil && (*task.Delay < 0 || *task.Delay > MaxDelaySeconds) {
		return fmt.Errorf("task %s sets delay %d: it is from 0 to %d seconds", label, *task.Delay, MaxDelaySeconds)
	}
	return nil
}

// collectionKind names the kind of v for an error message.
func collectionKind(v any) string { return collection.ValueKind(v) }
