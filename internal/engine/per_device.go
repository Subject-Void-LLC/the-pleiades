// Package engine: the one question of whether a task runs on each device on
// its own, and why.
//
// Most tasks are decided and rendered once and then dispatched to all of
// their devices together. Some cannot be: a rescue task reads its own device's
// failure, a loop runs its own items on each device, a condition reading the
// device or its stored facts has an answer per device, and so on. The run
// sends such a task down its per-device path, and that path cannot take
// lock_acquisition: all_at_plan_time, which takes every device's lock
// together.
//
// RunsPerDevice decides it for the run and for validation. The run asks it
// to choose the path (runNode, runGuarded), and validation asks it to refuse
// the lock before anything runs (internal/validate's lock_all.go), so a cause
// added here reaches both, and each cause carries its own reason
// (PerDeviceCause.LockAllReason), so both say the same words for one task.
// A new cause is a new constant below, a line in PerDeviceCauses and a reason;
// the tests beside this file and in internal/validate then require a case that
// runs it through validation and through a real run.
//
// Two refusals of the lock are made elsewhere and do not ask it. The builder
// refuses a handler's (validateHandlers, notify.go), from the runbook as
// written. And the builder refuses a background task's (async_validate.go),
// which is another question. The builder refuses no other cause, and must
// not: a rollback compiles the runbook the run ran, so a runbook that has
// run has to go on compiling (FAILURE_PATTERNS 1355's resolution).
package engine

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// PerDeviceCause is why a task runs on each device on its own.
type PerDeviceCause string

// NotPerDevice is the answer for a task that is decided once and dispatched
// to all of its devices together. It is not a cause, so it is not in
// PerDeviceCauses.
const NotPerDevice PerDeviceCause = ""

// The causes, in the order RunsPerDevice looks for them: a task with more
// than one is named by the first.
const (
	// PerDeviceRescue is a rescue task in a block that handles failures per
	// device: it runs only where its block failed and reads that device's
	// failure as error. A rescue task in a block that runs as one unit
	// (DAG.Units) is decided once for the unit and is not this cause.
	PerDeviceRescue PerDeviceCause = "rescue"
	// PerDeviceIteration is a task with loop:, with_items: or until:, whose
	// items and attempts each device runs for itself.
	PerDeviceIteration PerDeviceCause = "iteration"
	// PerDeviceHandler is a handler, which runs only on the devices whose
	// tasks notified it.
	PerDeviceHandler PerDeviceCause = "handler"
	// PerDeviceConditionUnreadable is a task whose condition's references
	// cannot be read, so it is decided on each device, which is right whatever
	// it reads. Every condition the builder compiles can be read, so this is a
	// backstop for a DAG assembled another way.
	PerDeviceConditionUnreadable PerDeviceCause = "condition_unreadable"
	// PerDeviceConditionReadsDevice is a task whose condition reads device.
	PerDeviceConditionReadsDevice PerDeviceCause = "condition_reads_device"
	// PerDeviceConditionReadsFacts is a task whose condition reads its
	// device's stored facts.
	PerDeviceConditionReadsFacts PerDeviceCause = "condition_reads_facts"
	// PerDeviceParamsReadDevice is a task with a parameter whose template
	// reads device.
	PerDeviceParamsReadDevice PerDeviceCause = "params_read_device"
	// PerDeviceVarsDiffer is a task reading vars where the variables can
	// differ by device: a task of the runbook sets facts, or a device of the
	// task's target brings variables of its own.
	PerDeviceVarsDiffer PerDeviceCause = "vars_differ"
)

// PerDeviceCauses returns every cause RunsPerDevice can answer with, in the
// order it looks for them. A test in this package holds it equal to the
// constants declared above, and one in internal/validate requires, for each,
// a runbook that validation and a real run both refuse in the same words.
func PerDeviceCauses() []PerDeviceCause {
	return []PerDeviceCause{
		PerDeviceRescue, PerDeviceIteration, PerDeviceHandler, PerDeviceConditionUnreadable,
		PerDeviceConditionReadsDevice, PerDeviceConditionReadsFacts, PerDeviceParamsReadDevice, PerDeviceVarsDiffer,
	}
}

// lockAllTail ends every reason a per-device task cannot take the lock.
const lockAllTail = ", so it cannot take lock_acquisition: all_at_plan_time, which takes every device's lock together"

// ErrDeviceRootLockAll is why a task reading the device root, in its
// condition or in a parameter, cannot take lock_acquisition:
// all_at_plan_time: it runs on each device on its own, so its devices' locks
// cannot all be taken together.
const ErrDeviceRootLockAll = "a task that reads device runs on each device on its own" + lockAllTail

// ErrIterationLockAll is why a task that loops or retries cannot take
// lock_acquisition: all_at_plan_time: each device runs its own items and
// attempts, so the task runs on each device on its own whatever it reads.
const ErrIterationLockAll = "a task with loop:, with_items: or until: runs its items and attempts on each device on its own" + lockAllTail

// ErrRescueLockAll is why a rescue task cannot take lock_acquisition:
// all_at_plan_time: it runs on each device on its own, reading that device's
// failure. Validation refuses it before anything runs and the run refuses it
// when it reaches the rescue, both for PerDeviceRescue. The builder does not:
// see the package comment above.
const ErrRescueLockAll = "a rescue task runs on each device on its own, reading that device's failure" + lockAllTail

// ErrVarsLockAll is why a task reading variables that differ by device cannot
// take lock_acquisition: all_at_plan_time. Such a task may read no device at
// all, so it does not borrow ErrDeviceRootLockAll's words.
const ErrVarsLockAll = "a task that reads vars is decided on each device on its own when the variables can differ by device (a task of this runbook sets a fact, or a device it targets has variables of its own)" + lockAllTail

// ErrFactsLockAll is why a task whose condition reads stored facts cannot
// take lock_acquisition: all_at_plan_time: a fact is its device's own.
const ErrFactsLockAll = "a task whose condition reads its device's stored facts is decided on each device on its own" + lockAllTail

// ErrUnreadableConditionLockAll is why a task whose condition cannot be read
// cannot take lock_acquisition: all_at_plan_time.
const ErrUnreadableConditionLockAll = "a task whose condition cannot be read for what it reads is decided on each device on its own" + lockAllTail

// LockAllReason is why a task that runs per device for cause c cannot take
// lock_acquisition: all_at_plan_time. The run's refusal and validation's
// finding both end with it. It is empty for NotPerDevice and
// for a value that is not a cause.
func (c PerDeviceCause) LockAllReason() string {
	switch c {
	case PerDeviceRescue:
		return ErrRescueLockAll
	case PerDeviceIteration:
		return ErrIterationLockAll
	case PerDeviceHandler:
		return ErrHandlerLockAll
	case PerDeviceConditionUnreadable:
		return ErrUnreadableConditionLockAll
	case PerDeviceConditionReadsDevice, PerDeviceParamsReadDevice:
		return ErrDeviceRootLockAll
	case PerDeviceConditionReadsFacts:
		return ErrFactsLockAll
	case PerDeviceVarsDiffer:
		return ErrVarsLockAll
	}
	return ""
}

// RunsPerDevice reports whether task, the node nodeID of dag, runs on each
// device on its own, and why. It is the run's own decision (runNode,
// runGuarded) and what validation refuses lock_acquisition: all_at_plan_time
// by, so neither holds a copy of the questions.
//
// The caller passes what it knows:
//
//   - eng is the engine the run renders parameters with. With none, no
//     parameter is rendered, so none is read for a device or a variable.
//   - devices returns the devices task's target names now, or none for a
//     target rendered from data. It is called only when the answer depends on
//     it: the task reads vars and no task of the runbook sets a fact. Every
//     caller has an inventory (the run its resolver, validation its world),
//     so it is never nil.
//
// Every cause but PerDeviceVarsDiffer is read from the runbook alone, as it
// is compiled and tag-selected. That one also reads the inventory, and the
// two callers read it differently on purpose: validation asks about every
// device the target names, and a run under --limit about those it reaches.
//
// What a run adds that no caller before it can know is whether the task is
// reached at all: a rescue task runs only where its block failed, and a check
// never runs one (runGuarded skips it before asking). A cause is what holds
// when the task runs.
func RunsPerDevice(dag *DAG, nodeID string, task *Task, eng render.Engine, devices func() []inventory.InventoryItem) PerDeviceCause {
	if rescuesPerDevice(dag, nodeID) {
		return PerDeviceRescue
	}
	// A loop or a retry runs on each device on its own (loop.go), and so does
	// a handler, which runs only where it was notified (notify.go).
	if task.iterates() {
		return PerDeviceIteration
	}
	if task.handler != nil {
		return PerDeviceHandler
	}
	conditional := dag.Conditions[nodeID] != nil
	if conditional {
		// A condition is read by its syntax tree, never its text, so a string
		// holding the word device does not count. One whose references cannot
		// be read is decided on each device: deciding it once would be wrong if
		// it read device.
		uses, err := ConditionUsesRoot(task.Conditional, TemplateRootDevice)
		if err != nil {
			return PerDeviceConditionUnreadable
		}
		if uses {
			return PerDeviceConditionReadsDevice
		}
		// A stored fact is the device's own (facts_root.go).
		if len(task.conditionFactKeys()) > 0 {
			return PerDeviceConditionReadsFacts
		}
	}
	if eng != nil && ParamsReadRoot(eng, task.Params, TemplateRootDevice) {
		return PerDeviceParamsReadDevice
	}
	if !readsVars(task, conditional, eng) {
		return NotPerDevice
	}
	if dag.UsesSetFact {
		return PerDeviceVarsDiffer
	}
	for _, d := range devices() {
		if len(deviceVars(d)) > 0 {
			return PerDeviceVarsDiffer
		}
	}
	return NotPerDevice
}

// rescuesPerDevice reports whether nodeID is a rescue task its devices each
// decide for themselves: it sits in the rescue of a block, at any depth, and
// no block around it runs as one unit (handlers.key).
func rescuesPerDevice(dag *DAG, nodeID string) bool {
	scopes := dag.Scopes[nodeID]
	if !InPart(scopes, PartRescue) {
		return false
	}
	for _, s := range scopes {
		if dag.Units[s.Block] {
			return false
		}
	}
	return true
}

// readsVars reports whether task reads the vars root: in its rollback: steps,
// which read what the task ran with on its device, in its condition (when
// conditional, the task having a compiled one), or in its parameters'
// templates. Without eng no template is read.
func readsVars(task *Task, conditional bool, eng render.Engine) bool {
	if eng != nil {
		for i := range task.Rollback {
			if ParamsReadRoot(eng, task.Rollback[i].Params, TemplateRootVars) {
				return true
			}
		}
	}
	if conditional {
		// RunsPerDevice has already answered for a condition that cannot be
		// read, so one reaching here parses.
		if uses, _ := ConditionUsesRoot(task.Conditional, TemplateRootVars); uses {
			return true
		}
	}
	return eng != nil && ParamsReadRoot(eng, task.Params, TemplateRootVars)
}

// perDevice is RunsPerDevice for this run: its renderer, and the devices
// task's target names now. It is where every cause a run acts on comes from:
// runPerDevice and rescueOn take the cause it returned, which a test holds
// true of every call (TestPerDeviceCause_OnlyPerDeviceSendsATaskDownThePath).
func (r *run) perDevice(nodeID string, task *Task) PerDeviceCause {
	return RunsPerDevice(r.dag, nodeID, task, r.x.renderer, func() []inventory.InventoryItem {
		target := TaskTarget(r.dag, task)
		if target == "" || HasTemplate(target) {
			return nil
		}
		devices, _ := ResolveTargets(r.x.resolver, target)
		return devices
	})
}

// refuseLockAll returns the error a task running per device for cause fails
// with when it takes lock_acquisition: all_at_plan_time, or nil when it does
// not take it.
func refuseLockAll(nodeID string, task *Task, cause PerDeviceCause) error {
	if task.LockAcquisition != AcquisitionAllAtPlanTime {
		return nil
	}
	return fmt.Errorf("%s: %s", taskLabel(nodeID, task), cause.LockAllReason())
}
