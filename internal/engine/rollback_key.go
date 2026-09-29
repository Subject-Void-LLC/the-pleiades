// Package engine: the runbook's own rollback keys, a task's rollback: list
// and the runbook's reversible: flag (Phase 40).
//
// A task's rollback: is its authored undo, the steps a rollback of a run
// runs on each device the task changed, in place of the undo the method
// recorded. It exists for what a recorded undo cannot express: a method
// that records none (exec.command), one whose undo withholds a value (a
// file's prior content), or an author who wants a different undo. The
// steps are plain method calls and nothing more, because they run long
// after the forward run, from a rollback command or job that has no
// register results, conditions or tags of its own to give them.
package engine

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// validateRollback holds a task's rollback: list to what a rollback can
// run: it belongs on a method call, not on a group, and each step is one
// plain method call on the task's own device.
func validateRollback(task *Task, id string) error {
	if len(task.Rollback) == 0 {
		return nil
	}
	switch {
	case task.Kind() != TaskKindLeaf:
		// A block's own id never runs (see validateTask), so no journal
		// entry could ever say it changed anything; and an import_tasks
		// task is a block by the time it is registered.
		return fmt.Errorf("task %s sets rollback: on a block or parallel group: put it on the tasks inside, since only a task that runs can be undone", taskLabel(id, task))
	case strings.Contains(id, ".rescue[") || strings.Contains(id, ".always["):
		return fmt.Errorf("task %s sets rollback: inside rescue: or always:, which does not run yet, so nothing it changes needs undoing; put rollback: on the tasks of the block", taskLabel(id, task))
	}
	for i := range task.Rollback {
		step := &task.Rollback[i]
		label := fmt.Sprintf("%s.rollback[%d]", id, i)
		if err := validateRollbackStep(step, label); err != nil {
			return err
		}
	}
	return nil
}

// validateRollbackStep refuses a rollback step that is anything but one
// method call with its own params.
func validateRollbackStep(step *Task, label string) error {
	if step.Kind() != TaskKindLeaf {
		return fmt.Errorf("rollback step %s must be one method call, not a block or parallel group", taskLabel(label, step))
	}
	var set []string
	for key, isSet := range map[string]bool{
		"register":         step.Register != "",
		"when":             len(step.When) > 0,
		"when_or":          len(step.WhenOr) > 0,
		"when_cel":         step.WhenCEL != "",
		"check_mode":       bool(step.CheckMode),
		"tags":             len(step.Tags) > 0,
		"register_mask":    len(step.RegisterMask) > 0,
		"secret_mask":      step.SecretMask != nil,
		"lock_acquisition": step.LockAcquisition != AcquisitionPerDeviceAsReached,
		"rollback":         len(step.Rollback) > 0,
	} {
		if isSet {
			set = append(set, key)
		}
	}
	if len(set) > 0 {
		slices.Sort(set)
		return fmt.Errorf("rollback step %s sets %s: a rollback step is a method and its params, run on the device the task changed, when a rollback runs; a rollback has no register results, conditions or tags to give it",
			taskLabel(label, step), strings.Join(set, ", "))
	}
	// The device is the one the task changed, which the rollback names; a
	// step naming another would undo on a device the run never touched.
	if _, present := step.Params[collection.TargetParam]; present {
		return fmt.Errorf("rollback step %s sets params.%s: a rollback step runs on the device the task changed, and cannot name another", taskLabel(label, step), collection.TargetParam)
	}
	return nil
}

// forwardDefinition returns def with its rollback keys cleared: every
// task's rollback: list and the runbook's reversible: flag. DAG.Version is
// hashed over it, so a version names what the forward run does and
// nothing else. Adding a rollback: step after a run failed, which is the
// ordinary way to write one, is then not drift from the run being undone;
// and a runbook with neither key hashes exactly as it did before they
// existed (both are omitempty).
func forwardDefinition(def WorkflowDef) WorkflowDef {
	def.Reversible = false
	def.PreTasks = withoutRollback(def.PreTasks)
	def.Tasks = withoutRollback(def.Tasks)
	def.PostTasks = withoutRollback(def.PostTasks)
	return def
}

// withoutRollback copies tasks, recursively, with every Rollback cleared.
// It copies rather than editing in place, since the caller's tasks are
// the ones the DAG keeps.
func withoutRollback(tasks []Task) []Task {
	if tasks == nil {
		return nil
	}
	out := make([]Task, len(tasks))
	for i, t := range tasks {
		t.Rollback = nil
		t.Block = withoutRollback(t.Block)
		t.Rescue = withoutRollback(t.Rescue)
		t.Always = withoutRollback(t.Always)
		t.Parallel = withoutRollback(t.Parallel)
		out[i] = t
	}
	return out
}
