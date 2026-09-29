// Package engine: check mode, the run that reports what would change and
// changes nothing (PLAN.md Section 34's typed execution mode, and the
// "simulate" of Section 9 made into a parameter).
//
// Three rules shape everything in this file, and each is the reason a
// check can be trusted rather than merely run:
//
//  1. Only code that declared it can answer is asked. An ActionExecutor
//     that does not implement CheckExecutor is never called in check mode,
//     and a Collection method is checked only through its own declared
//     Check function. Nothing here infers "this looks read-only" from a
//     name.
//  2. What cannot be checked is named, never counted as a success. Such a
//     task becomes an UncheckedError, the node is reported as unchecked
//     with its reason, and the walk carries on so every other task still
//     gets its answer. A dry run that quietly skipped what it could not
//     model has reported a clean result for a plan it never checked.
//  3. A check leaves no record that something was done. It never writes the
//     run journal, and a check result carrying an undo instruction is
//     refused, because a later rollback reading an undo for a change that
//     never happened would change the device for real.
package engine

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// CheckExecutor is implemented by an ActionExecutor that can run a task in
// check mode: reach the device, work out whether a real run would change
// anything, and change nothing.
//
// It is a separate interface rather than a second method on
// ActionExecutor so that an executor gains check support by choosing to,
// and every existing executor (including every test fake) stays exactly
// as it was: an executor that does not implement it is simply one whose
// tasks are reported as unchecked.
type CheckExecutor interface {
	// Check is Execute's check-mode counterpart. It returns an
	// *UncheckedError, rather than a plain error, for a task it cannot
	// check, so the Executor can tell "this cannot be checked" apart from
	// "checking this found a real problem".
	Check(ctx context.Context, task *Task, device inventory.InventoryItem) (ActionResult, error)
}

// UncheckedError reports that a task's action cannot be run in check
// mode. It is the action's own answer, not a failure of the run: the node
// is reported as unchecked, by name and with this reason, and the rest of
// the runbook is still checked.
type UncheckedError struct {
	// FQCN is the action that could not be checked.
	FQCN string

	// Reason says why, in words an operator can act on.
	Reason string
}

// Error implements error.
func (e *UncheckedError) Error() string {
	return fmt.Sprintf("%s cannot be checked: %s", e.FQCN, e.Reason)
}

// checkThrough asks next to check task when next can, and otherwise
// reports task as unchecked.
//
// It is how a composing executor hands on a task it does not own, the
// check-mode twin of calling fallback.Execute. Every composing executor
// uses it, so "the layer below cannot check this" is decided in exactly
// one place.
func checkThrough(ctx context.Context, next ActionExecutor, task *Task, device inventory.InventoryItem) (ActionResult, error) {
	if checker, ok := next.(CheckExecutor); ok {
		return checker.Check(ctx, task, device)
	}
	return ActionResult{}, &UncheckedError{FQCN: task.FQCN, Reason: "no part of this run knows how to check it"}
}

// WithMode sets how this Executor runs every task: collection.ModeExecute
// (the default) applies changes, and collection.ModeCheck reports what
// would change and applies nothing.
//
// A value outside that closed set is not corrected to either one. Every
// task then fails with a message naming the mode, because silently
// treating an unknown mode as execute is the one mistake that changes a
// device the caller asked only to look at.
func WithMode(mode collection.Mode) ExecutorOption {
	return func(x *Executor) { x.mode = mode }
}

// WithExternalChecks sets whether a check may run an external Collection
// program's Check at all. Off by default: a third party's Check is code
// nothing has proven only reads, so running it is a decision whoever
// launched the check must be entitled to, which is whoever may run the
// same thing for real. The command line turns it on, since its user runs
// those programs for real anyway; the Runner turns it on only for a job
// whose launcher held runbook:execute (wire.DispatchPayload.ExternalChecks).
// Off, such a task is reported unchecked, naming why.
func WithExternalChecks(allowed bool) ExecutorOption {
	return func(x *Executor) { x.externalChecks = allowed }
}

// checkAction runs cmd's task in check mode and applies the one rule every
// check result must meet whoever produced it: it carries no undo
// instruction.
//
// The rule is enforced here, on the result, rather than inside each
// executor, so it holds for a method checked in this process, in a child
// process and in a third-party program alike. A Check function is told
// not to record one (collection.Descriptor.Check's own doc comment); this
// is what makes that instruction a guarantee.
func (r *run) checkAction(ctx context.Context, cmd nodeExecution) (ActionResult, error) {
	checker, ok := r.x.actions.(CheckExecutor)
	if !ok {
		return ActionResult{}, &UncheckedError{FQCN: cmd.Task.FQCN, Reason: "no part of this run knows how to check it"}
	}
	if p := externalProvider(cmd.Task.FQCN); p != nil && !r.x.externalChecks {
		return ActionResult{}, &UncheckedError{FQCN: cmd.Task.FQCN, Reason: fmt.Sprintf(
			"this method's check comes from the external program %s, which nothing has proven only reads, and this check was launched without the right to run it for real (runbook:execute)",
			p.Program)}
	}
	if cmd.Device != nil && cmd.Device.State() == inventory.StateSimulateLocked {
		if p := externalProvider(cmd.Task.FQCN); p != nil {
			return ActionResult{}, &UncheckedError{FQCN: cmd.Task.FQCN, Reason: fmt.Sprintf(
				"device %q is simulate-locked, and this method's check comes from the external program %s, which nothing has proven only reads, so it is not run against that device",
				cmd.Device.Name(), p.Program)}
		}
	}

	result, err := checker.Check(ctx, cmd.Task, cmd.Device)
	if err != nil {
		// The credential the check was handed is masked even when it failed.
		return ActionResult{Secrets: result.Secrets}, err
	}
	if _, recorded := result.Stats[sdk.StatInverse]; recorded {
		return ActionResult{Secrets: result.Secrets}, fmt.Errorf("%s: its check recorded an undo instruction (the %q stat), which a check must never do: nothing was changed, so there is nothing to undo",
			cmd.Task.FQCN, sdk.StatInverse)
	}
	result.Stats = stampPrediction(result.Stats)
	return result, nil
}

// stampPrediction marks a check's result as a prediction (sdk.StatPredicted),
// and its diff too when it recorded one (sdk.DiffPredicted), whatever the
// method itself set. The engine does it, because the engine knows the
// mode: a method cannot forget, and cannot claim otherwise. stats is never
// modified; a stamped copy is returned.
func stampPrediction(stats map[string]any) map[string]any {
	stamped := make(map[string]any, len(stats)+1)
	for k, v := range stats {
		stamped[k] = v
	}
	if diff, ok := stamped[sdk.StatDiff].(map[string]any); ok {
		copied := make(map[string]any, len(diff)+1)
		for k, v := range diff {
			copied[k] = v
		}
		copied[sdk.DiffPredicted] = true
		stamped[sdk.StatDiff] = copied
	}
	stamped[sdk.StatPredicted] = true
	return stamped
}

// refusePrediction refuses a real run's result that claims to be a
// prediction (sdk.StatPredicted, or sdk.DiffPredicted inside its diff): a
// method may never set either, and a real run that did would misstate
// what happened to every reader of its result.
func refusePrediction(fqcn string, stats map[string]any) error {
	_, claimed := stats[sdk.StatPredicted]
	if diff, ok := stats[sdk.StatDiff].(map[string]any); ok {
		if _, inDiff := diff[sdk.DiffPredicted]; inDiff {
			claimed = true
		}
	}
	if claimed {
		return fmt.Errorf("%s: its real run's result says it is a prediction (the %q stat), which only the engine sets, and only on a check", fqcn, sdk.StatPredicted)
	}
	return nil
}

// externalProvider returns the external program providing fqcn's method,
// or nil for a method compiled into this binary or no registered method
// at all.
//
// A simulate-locked device (PLAN.md Section 9) may be checked and never
// run against, and admitting it to a check rests on a check only reading.
// That is proven for every built-in check, each tested against its real
// run, and for nothing a third party wrote: its Check could write, and
// the engine sees only what the check reports. So a check from an
// external program is not run against such a device and is reported
// unchecked instead (FAILURE_PATTERNS 253). The provider comes from
// collection.Descriptor.Provider, which only the loader sets.
func externalProvider(fqcn string) *collection.Provider {
	if d, ok := collection.Lookup(fqcn); ok {
		return d.Provider
	}
	return nil
}

// LifecycleAdmitsIn is LifecycleAdmits with the one exception check mode
// makes: a simulate-locked device is admitted to a check.
//
// PLAN.md Section 9 defines simulate-locked as a device that may be
// simulated and may not be run against for real until an administrator
// unlocks it, and the sync plugins already put every newly discovered
// device into that state. A check changes nothing, so it is exactly what
// such a device is for. In execute mode the answer is LifecycleAdmits'
// own, unchanged: the lock still cannot be escalated.
//
// LifecycleAdmits itself is left alone because the Controller's dispatch
// admission calls it too, and that path has no mode to consult.
//
// It is exported for internal/validate's LifecycleRule, the plan-time half
// of the same check, so the exception has exactly one definition: a plan
// that validation admitted is never refused by the executor for the same
// device, and the reverse.
func LifecycleAdmitsIn(mode collection.Mode, dev inventory.InventoryItem) (bool, string) {
	if mode == collection.ModeCheck && dev.State() == inventory.StateSimulateLocked {
		return true, ""
	}
	return LifecycleAdmits(dev)
}
