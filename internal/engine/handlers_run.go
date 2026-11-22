// Package engine: running a task inside a block that has a rescue: or an
// always:, which decides per device before anything else does.
package engine

import (
	"context"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// conditionalSkipReason is a rescue task's reason in a check.
const conditionalSkipReason = "runs only if its block fails, which a check does not predict"

// runGuarded runs nodeID, a task inside a block with a rescue: or an
// always:.
//
// In a block that runs as one unit (DAG.Units) the decision is the unit's,
// the same for every device, so it is made once, and the task then renders,
// resolves its own target and runs as any task does, a rescue task reading
// the unit's failure as error.
//
// Otherwise each device decides: its devices come from its authored target
// (a task on the runbook's hosts renders none), so a device it does not run
// on renders and evaluates nothing, and a register the failed task never
// wrote cannot fail a task that would not have run. A failure of its
// condition or parameters is each device's it would have run on. A rescue
// task renders and evaluates its condition per device, since the failure it
// reads as error is that device's own.
//
// In a check a rescue task is not predicted (conditional_skip): a check
// never runs a fallback.
func (r *run) runGuarded(ctx context.Context, nodeID string, task *Task, started time.Time) []NodeResult {
	rescue := InPart(r.dag.Scopes[nodeID], PartRescue)
	if rescue && r.modeFor(task) == collection.ModeCheck {
		r.publish(nodeID, task, "", "skipped", conditionalSkipReason)
		return []NodeResult{finish(NodeResult{
			NodeID: nodeID, StartedAt: started, Skipped: true, Checked: true, Conditional: true, SkipReason: conditionalSkipReason,
		})}
	}
	mask := func(s string) string { return redact.Text(r.secrets.Snapshot(), s) }

	if unit := r.handlers.key(nodeID, ""); strings.HasPrefix(unit, "unit:") {
		if ok, why := r.handlers.decide(nodeID, ""); !ok {
			return []NodeResult{r.handlerSkip(nodeID, task, nil, started, why)}
		}
		var errorRoot map[string]any
		if rescue {
			errorRoot = r.handlers.errorContext(nodeID, "", r.dag, mask)
		}
		// A unit's task that reads device, or runs per device for another
		// cause, still runs on each device, every one reading the unit's
		// failure. Being a unit's rescue task is not such a cause.
		if cause := r.perDevice(nodeID, task); cause != NotPerDevice {
			return r.runPerDevice(ctx, nodeID, task, started, errorRoot, cause)
		}
		prepared, early := r.prepare(ctx, nodeID, task, started, "", errorRoots(errorRoot))
		if early != nil {
			return []NodeResult{*early}
		}
		devices, err := r.resolveDevices(prepared)
		if err != nil {
			return []NodeResult{r.unresolved(nodeID, prepared, started, err)}
		}
		return r.dispatch(ctx, nodeID, prepared, devices, started)
	}

	devices, err := r.resolveDevices(task)
	if err != nil {
		return []NodeResult{r.unresolved(nodeID, task, started, err)}
	}

	var results []NodeResult
	var run []inventory.InventoryItem
	runsWithNoDevice := false
	if len(devices) == 0 {
		if ok, why := r.handlers.decide(nodeID, ""); ok {
			runsWithNoDevice = true
		} else {
			results = append(results, r.handlerSkip(nodeID, task, nil, started, why))
		}
	}
	for _, d := range devices {
		if ok, why := r.handlers.decide(nodeID, string(d.ID())); ok {
			run = append(run, d)
		} else {
			results = append(results, r.handlerSkip(nodeID, task, d, started, why))
		}
	}
	if len(run) == 0 && !runsWithNoDevice {
		return results
	}

	// Here, outside a unit, being a rescue task is itself a cause
	// (PerDeviceRescue), so one never takes this path.
	cause := r.perDevice(nodeID, task)
	if cause == NotPerDevice {
		prepared, early := r.prepare(ctx, nodeID, task, started, "", nil)
		switch {
		case early != nil && early.Err != nil && len(run) > 0:
			// The task's own condition or parameters failed: that is each
			// device's failure it would have run on, for its block to answer.
			for _, d := range run {
				failed := *early
				failed.Device = string(d.ID())
				results = append(results, failed)
			}
			return results
		case early != nil:
			return append(results, *early)
		}
		return append(results, r.dispatch(ctx, nodeID, prepared, run, started)...)
	}

	// A rescue task, each device reading its own failure, a task that loops
	// or retries, or a task reading the device it runs on. Each runs on each
	// device on its own, so none can take every device's lock together.
	if wrapped := refuseLockAll(nodeID, task, cause); wrapped != nil {
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		return append(results, failedNode(nodeID, started, FailureStageLockAll, wrapped))
	}
	if runsWithNoDevice {
		return append(results, r.rescueOn(ctx, nodeID, task, nil, started, mask, cause)...)
	}
	for _, out := range runConcurrently(run, func(d inventory.InventoryItem) []NodeResult {
		return r.rescueOn(ctx, nodeID, task, d, started, mask, cause)
	}) {
		results = append(results, out...)
	}
	return results
}

// rescueOn runs task nodeID on d alone (nil for a task with no device),
// with d as its device root, d's own variables in its vars, as a top-level
// task has them, and, for a rescue task, d's failure as its error root.
// cause is why the task runs per device (run.perDevice): outside a unit a
// rescue task's is always PerDeviceRescue, which is how it is known here.
func (r *run) rescueOn(ctx context.Context, nodeID string, task *Task, d inventory.InventoryItem, started time.Time, mask func(string) string, cause PerDeviceCause) []NodeResult {
	key := ""
	var devices []inventory.InventoryItem
	if d != nil {
		key = string(d.ID())
		devices = []inventory.InventoryItem{d}
	}
	roots := deviceRoots(d)
	if cause == PerDeviceRescue {
		if e := r.handlers.errorContext(nodeID, key, r.dag, mask); e != nil {
			roots[TemplateRootError] = e
		}
	}
	return r.runTask(ctx, nodeID, task, started, key, devices, roots)
}

// handlerSkip is nodeID's result on d (nil for no device) when the device's
// handling keeps it from running there.
func (r *run) handlerSkip(nodeID string, task *Task, d inventory.InventoryItem, started time.Time, why skip) NodeResult {
	host, device := "", ""
	if d != nil {
		host, device = d.Name(), string(d.ID())
	}
	r.publish(nodeID, task, host, "skipped", why.reason)
	return finish(NodeResult{NodeID: nodeID, Device: device, StartedAt: started, Skipped: true, SkipReason: why.reason, skipKind: why.kind})
}
