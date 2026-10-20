// Package engine: running a task inside a block that has a rescue: or an
// always:, which decides per device before anything else does.
package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// conditionalSkipReason is a rescue task's reason in a check.
const conditionalSkipReason = "runs only if its block fails, which a check does not predict"

// runGuarded runs nodeID, a task inside a block with a rescue: or an
// always:. Its devices come from its authored target (a guarded task may
// not render one, chain.go), so each device's handling decides first
// whether the task runs there at all, and a device it does not run on
// renders and evaluates nothing: a register the failed task never wrote
// cannot fail a task that would not have run.
//
// A rescue task renders and evaluates its condition per device, since the
// failure it reads as error is that device's own. In a check a rescue task
// is not predicted (conditional_skip): a check never runs a fallback.
func (r *run) runGuarded(ctx context.Context, nodeID string, task *Task, started time.Time) []NodeResult {
	rescue := InPart(r.dag.Scopes[nodeID], PartRescue)
	if rescue && r.modeFor(task) == collection.ModeCheck {
		r.publish(nodeID, task, "", "skipped", conditionalSkipReason)
		return []NodeResult{finish(NodeResult{
			NodeID: nodeID, StartedAt: started, Skipped: true, Checked: true, Conditional: true, SkipReason: conditionalSkipReason,
		})}
	}

	devices, err := r.resolveDevices(task)
	if err != nil {
		wrapped := fmt.Errorf("failed to resolve target for %s: %w", taskLabel(nodeID, task), err)
		r.publish(nodeID, task, "", "failed", wrapped.Error())
		return []NodeResult{failedNode(nodeID, started, FailureStageResolveTarget, wrapped)}
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

	if !rescue {
		prepared, early := r.prepare(nodeID, task, started, "", nil)
		if early != nil {
			return append(results, *early)
		}
		return append(results, r.dispatch(ctx, nodeID, prepared, run, started)...)
	}

	// A rescue task: each device reads its own failure.
	mask := func(s string) string { return redact.Text(r.secrets.Snapshot(), s) }
	if runsWithNoDevice {
		return append(results, r.rescueOn(ctx, nodeID, task, nil, started, mask)...)
	}
	for _, out := range runConcurrently(run, func(d inventory.InventoryItem) []NodeResult {
		return r.rescueOn(ctx, nodeID, task, d, started, mask)
	}) {
		results = append(results, out...)
	}
	return results
}

// rescueOn runs rescue task nodeID on d (nil for a task with no device),
// with d's failure as its error root.
func (r *run) rescueOn(ctx context.Context, nodeID string, task *Task, d inventory.InventoryItem, started time.Time, mask func(string) string) []NodeResult {
	key := ""
	var devices []inventory.InventoryItem
	if d != nil {
		key = string(d.ID())
		devices = []inventory.InventoryItem{d}
	}
	prepared, early := r.prepare(nodeID, task, started, key, r.handlers.errorContext(key, r.dag, mask))
	if early != nil {
		return []NodeResult{*early}
	}
	return r.dispatch(ctx, nodeID, prepared, devices, started)
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
