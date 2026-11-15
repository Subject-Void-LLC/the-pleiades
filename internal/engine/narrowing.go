// Package engine: a run narrowed to some of the inventory's devices (the
// CLI's --limit, a serial: batch), and the one rule that tells a target name
// whose devices the narrowing hid from a name that matches nothing.
//
// A name of a task's target (its target:, or the runbook's hosts:) whose
// every device the narrowing hid is not a mistake in the runbook: the run was
// asked to reach fewer devices, and this task's are not among them. Such a
// task is skipped on this run, naming the devices and why, rather than failed
// as a target that "matches no inventory host or tag", which validate never
// said and which is not true (run 10). A name that matches nothing in the
// whole inventory is still missing, narrowed or not, and still fails.
package engine

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Narrowing is implemented by a TargetResolver that hides some of the
// inventory's devices from a run: the CLI's --limit keeps only the hosts the
// limit admits, and a serial: batch keeps only the batch running.
type Narrowing interface {
	// Unnarrowed resolves target as the resolver would if it hid nothing.
	Unnarrowed(target string) []inventory.InventoryItem
	// HiddenBecause says why the run does not reach d, in words a skip reason
	// carries (`outside --limit "CORE1"`), or "" when it does.
	HiddenBecause(d inventory.InventoryItem) string
}

// HiddenName is one name of a target whose devices are all in the
// inventory and all hidden from this run.
type HiddenName struct {
	// Name is the name as the target writes it.
	Name string
	// Devices are the names of the devices it matches, sorted.
	Devices []string
	// Why is the narrowing's reason for each device, in Devices' order.
	Why []string
}

// Resolution is what a target resolves to in one run: the devices it
// reaches, the names that match nothing in the inventory, and the names whose
// every device the run's narrowing hid.
type Resolution struct {
	Devices []inventory.InventoryItem
	Missing []string
	Hidden  []HiddenName
}

// NarrowedAway reports whether the target reaches no device only because the
// run's narrowing hid every one it names: nothing is missing, and something
// is hidden.
func (res Resolution) NarrowedAway() bool {
	return len(res.Devices) == 0 && len(res.Missing) == 0 && len(res.Hidden) > 0
}

// ResolveTargetsIn resolves target as ResolveTargets does and, when r is a
// Narrowing, moves each missing name that matches devices in the whole
// inventory from Missing to Hidden.
func ResolveTargetsIn(r TargetResolver, target string) Resolution {
	devices, missing := resolveTargetNames(r, target)
	res := Resolution{Devices: devices, Missing: missing}
	n, ok := r.(Narrowing)
	if !ok || len(missing) == 0 {
		return res
	}
	// A target holding a comma is asked whole first (ResolveTargets), so a
	// device or tag whose own name holds one, all of it hidden, is hidden as
	// that one name rather than missing as its pieces.
	if len(devices) == 0 && len(splitHosts(target)) >= 2 {
		if whole := n.Unnarrowed(target); len(whole) > 0 {
			return Resolution{Hidden: []HiddenName{hiddenName(n, target, whole)}}
		}
	}
	res.Missing = nil
	for _, name := range missing {
		if all := n.Unnarrowed(name); len(all) > 0 {
			res.Hidden = append(res.Hidden, hiddenName(n, name, all))
			continue
		}
		res.Missing = append(res.Missing, name)
	}
	return res
}

// hiddenName records name, matching devices in the whole inventory, as
// hidden, with the narrowing's reason for each device.
func hiddenName(n Narrowing, name string, devices []inventory.InventoryItem) HiddenName {
	sorted := append([]inventory.InventoryItem(nil), devices...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name() < sorted[j].Name() })
	h := HiddenName{Name: name}
	for _, d := range sorted {
		why := n.HiddenBecause(d)
		if why == "" {
			// The narrowed resolver answered nothing for a device it does
			// not say it hides: a resolver that disagrees with itself.
			why = "not reached by this run"
		}
		h.Devices = append(h.Devices, d.Name())
		h.Why = append(h.Why, why)
	}
	return h
}

// narrowedTarget is the error a task's devices resolve to when the run's
// narrowing hid every device its target names. It is a skip, not a failure,
// and every caller of resolveDevices turns it into one (unresolved).
type narrowedTarget struct {
	target string
	hidden []HiddenName
}

// Error implements error, and is the skip's reason.
func (n *narrowedTarget) Error() string {
	// The devices are grouped by why the run does not reach them, in the
	// order the target names them, so a --limit reads as one clause.
	var order []string
	byWhy := map[string][]string{}
	for _, h := range n.hidden {
		for i, d := range h.Devices {
			why := h.Why[i]
			if _, seen := byWhy[why]; !seen {
				order = append(order, why)
			}
			byWhy[why] = append(byWhy[why], d)
		}
	}
	parts := make([]string, 0, len(order))
	for _, why := range order {
		parts = append(parts, fmt.Sprintf("%s is %s", strings.Join(byWhy[why], ", "), why))
	}
	return fmt.Sprintf("target %q names no device this run reaches: %s", n.target, strings.Join(parts, "; "))
}

// narrowedAway returns the narrowedTarget for task when its target is
// written out (not rendered from data, which within: decides after the
// render) and every device it names is hidden from this run, and nil
// otherwise. runNode asks it before anything else, so a task the run does not
// reach is skipped before its condition or its parameters are read: they may
// read a result that an earlier task, skipped the same way, never wrote.
func (r *run) narrowedAway(task *Task) *narrowedTarget {
	if task.Within != "" {
		return nil
	}
	// resolveDevices is the one place that decides it, so this answer and
	// the one a task gets when it resolves its devices cannot differ.
	var narrowed *narrowedTarget
	if _, err := r.resolveDevices(task); errors.As(err, &narrowed) {
		return narrowed
	}
	return nil
}

// unresolved is nodeID's result when its devices did not resolve: the
// narrowed result (narrowedResult) when the run's narrowing hid every device
// its target names, and a failure to resolve its target otherwise.
func (r *run) unresolved(nodeID string, task *Task, started time.Time, err error) NodeResult {
	var narrowed *narrowedTarget
	if errors.As(err, &narrowed) {
		return r.narrowedResult(nodeID, task, started, narrowed)
	}
	wrapped := fmt.Errorf("failed to resolve target for %s: %w", taskLabel(nodeID, task), err)
	r.publish(nodeID, task, "", "failed", wrapped.Error())
	return failedNode(nodeID, started, FailureStageResolveTarget, wrapped)
}

// narrowedResult is nodeID's result on a run that does not reach any device
// its target names. It is a skip naming the devices and why, except for a
// rescue task while its block is handling a failure: a rescue that cannot run
// answers nothing, so it fails, and the failure it was to answer stands
// (fail closed). Skipping it would leave the rescue "finished without
// failing", which reads as rescued, and a run that did nothing would exit 0.
// An always: task is a skip whatever its block is doing, since it answers no
// failure; its skip line names the devices and the limit or batch.
func (r *run) narrowedResult(nodeID string, task *Task, started time.Time, n *narrowedTarget) NodeResult {
	if r.rescueWanted(nodeID) {
		err := fmt.Errorf("rescue task %s cannot run, so it does not answer the failure its block is handling: %w", taskLabel(nodeID, task), n)
		r.publish(nodeID, task, "", "failed", err.Error())
		// A resolve_target failure fails every device of the block alike and
		// ends the run, the always: of its blocks still running (handlers.go
		// failed), as a rescue whose target matched nothing always did.
		return failedNode(nodeID, started, FailureStageResolveTarget, err)
	}
	reason := n.Error()
	r.publish(nodeID, task, "", "skipped", reason)
	return finish(NodeResult{NodeID: nodeID, StartedAt: started, Skipped: true, SkipReason: reason, skipKind: SkipKindLimit})
}

// rescueWanted reports whether nodeID is a rescue task that would run now for
// some handling: its block, or a block it is inside, is answering a failure
// there (handlers.decide). The handlers are read only at the level barrier's
// side, never written while a level runs, so reading them here is safe.
func (r *run) rescueWanted(nodeID string) bool {
	if !InPart(r.dag.Scopes[nodeID], PartRescue) {
		return false
	}
	if key := r.handlers.key(nodeID, ""); key != "" {
		// A unit block keys every device together.
		ok, _ := r.handlers.decide(nodeID, "")
		return ok
	}
	for key := range r.handlers.frames {
		if ok, _ := r.handlers.decide(nodeID, key); ok {
			return true
		}
	}
	return false
}
