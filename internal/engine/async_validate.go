// Package engine: the build-time rules for async and poll (async.go): what
// one task may say, and that a background task is collected.
package engine

import (
	"fmt"
	"strings"
)

// asyncRefusedFor names the engine actions async cannot apply to: each runs
// in the engine at once, or is async_status itself.
var asyncRefusedFor = map[string]bool{
	"set_fact": true, SetFactFQCN: true, "assert": true, AssertFQCN: true, "fail": true, FailFQCN: true,
	PauseName: true, PauseFQCN: true, "set_metadata": true, "pleiades.builtin.set_metadata": true,
	AsyncStatusName: true, AsyncStatusFQCN: true,
}

// validateAsyncKeys refuses async and poll that cannot mean what they say on
// one task. label names the task.
func validateAsyncKeys(t *Task, label string) error {
	if t.Async == 0 && t.Poll == nil {
		return nil
	}
	switch {
	case t.Async == 0:
		return fmt.Errorf("%s: poll needs async, the limit it polls within", label)
	case t.Async < 0 || int(t.Async) > MaxTaskTimeout:
		return fmt.Errorf("%s: async must be from 1 to %d seconds, not %d", label, MaxTaskTimeout, t.Async)
	case t.Kind() != TaskKindLeaf:
		return fmt.Errorf("%s: async goes on a task that calls a method, not on a block or a parallel group", label)
	case asyncRefusedFor[t.FQCN]:
		return fmt.Errorf("%s: %s runs in the engine at once, so async does not apply to it", label, t.FQCN)
	case t.Poll != nil && *t.Poll < 0:
		return fmt.Errorf("%s: poll must be 0 (run in the background) or a number of seconds", label)
	case t.Poll != nil && *t.Poll > t.Async:
		return fmt.Errorf("%s: poll %d is longer than the async limit %d it polls within", label, *t.Poll, t.Async)
	case t.iterates():
		return fmt.Errorf("%s: async with a loop or a retry is not supported; give each item a task of its own", label)
	}
	if t.background() {
		switch {
		case t.Register == "":
			return fmt.Errorf("%s: a background task (poll: 0) needs a register, which holds the job id an async_status collects it by", label)
		case t.LockAcquisition == AcquisitionAllAtPlanTime:
			return fmt.Errorf("%s: a background task (poll: 0) cannot take lock_acquisition: all_at_plan_time", label)
		}
	}
	return nil
}

// validateAsyncFlow refuses a background task no later task collects: its
// result would be lost, so validation asks for an async_status reading its
// register's job id after it, in the same runbook.
//
// It also refuses a background task inside a block with rollback_on_failure:
// that block's undo restores the device when it fails, while the job would go
// on changing the device after the undo, and is journaled only when collected.
func validateAsyncFlow(def *WorkflowDef) error {
	var order []*Task
	var refused error
	var walk func([]Task, string)
	walk = func(tasks []Task, undoneBy string) {
		for i := range tasks {
			t := &tasks[i]
			order = append(order, t)
			if refused == nil && undoneBy != "" && t.background() {
				refused = fmt.Errorf("task %q runs in the background (poll: 0) inside block %q, which has rollback_on_failure: the block's undo cannot undo a job still running; poll the task (poll: N) or move it out of the block", t.Name, undoneBy)
			}
			// The tasks of a block that undoes itself on failure are held to
			// that block's undo; its rescue and always run after the undo,
			// so they are held only to an enclosing block's.
			inner := undoneBy
			if t.RollbackOnFailure {
				inner = t.Name
			}
			walk(t.Block, inner)
			walk(t.Rescue, undoneBy)
			walk(t.Always, undoneBy)
			walk(t.Parallel, undoneBy)
		}
	}
	walk(def.PreTasks, "")
	walk(def.Tasks, "")
	walk(def.PostTasks, "")
	walk(def.Handlers, "")
	if refused != nil {
		return refused
	}
	for i, t := range order {
		// A background task with no register is refused on its own, naming
		// that (validateAsyncKeys).
		if !t.background() || t.Register == "" {
			continue
		}
		collected := false
		for _, later := range order[i+1:] {
			if isAsyncStatus(later.FQCN) && strings.Contains(fmt.Sprint(later.Params["jid"]), "result."+t.Register+".") {
				collected = true
				break
			}
		}
		if !collected {
			return fmt.Errorf("task %q runs in the background (poll: 0) and no later async_status collects it: add one with jid: \"{{ result.%s.%s }}\", or its result is lost", t.Name, t.Register, StatJobID)
		}
	}
	return nil
}
