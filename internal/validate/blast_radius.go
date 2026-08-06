package validate

import (
	"sort"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
)

// BlastRadius summarizes how large an impact running a runbook right now
// would have, in terms of the current inventory: how many distinct devices
// it targets, and which inventory tiers those devices declare.
//
// Unlike Metadata.ServiceEffecting (an author-set flag hand-written into
// the runbook YAML/JSON), neither DeviceCount nor Tiers is ever authored by
// hand: both are computed by the engine from the runbook's resolved
// targets and whatever the inventory currently looks like, so the same
// runbook can yield a different BlastRadius on a later call if the
// inventory changed in between. BlastRadius therefore has no YAML/JSON
// representation of its own; it is a value CalculateBlastRadius returns,
// never a field a caller decodes from a runbook document.
type BlastRadius struct {
	// DeviceCount is the number of distinct inventory devices targeted by
	// any task in the runbook (pretasks, tasks, posttasks, and any
	// block/rescue/always descendant at any nesting depth), deduplicated
	// by device ID: a device targeted by more than one task is still
	// counted once.
	DeviceCount int

	// Tiers is the sorted, deduplicated set of distinct values found on
	// the "tier" property of any targeted device. It is deliberately a
	// plain set rather than a ranked "highest tier" value: this codebase
	// has no defined tier taxonomy or ordering yet (e.g. no established
	// ranking of "prod" above "lab" to reduce Tiers down to a single
	// worst-case value against), so that ranking is a real, separate
	// follow-up decision, not something to invent here. Tiers is empty if
	// no targeted device declares a "tier" property at all.
	Tiers []string
}

// CalculateBlastRadius computes a BlastRadius for world's runbook against
// world's current inventory. It walks world.DAG.Nodes, the same flattened
// map CapabilityRule walks (every task in the runbook, including every
// block/rescue/always descendant, keyed by synthesized ID), resolves every
// task's non-empty effective target (engine.TaskTarget) via world.Resolve,
// and accumulates:
//
//   - the distinct set of resolved devices, keyed by InventoryItem.ID() so
//     a device targeted by more than one task is never double-counted; and
//   - the distinct, sorted set of "tier" property values (via
//     Properties.String("tier")) declared by any device in that set.
//
// Like BlastRadius itself, this is never authored by hand: it is always
// derived fresh from the runbook and current inventory state at call time,
// not registered as a validate.Rule since it is informational data, not a
// pass/fail check. Callers invoke it directly.
func CalculateBlastRadius(world WorldView) BlastRadius {
	devices := make(map[string]struct{})
	tiers := make(map[string]struct{})

	for _, task := range world.DAG.Nodes {
		target := engine.TaskTarget(world.DAG, task)
		if target == "" {
			continue
		}

		for _, item := range world.Resolve(target) {
			id := string(item.ID())
			if _, seen := devices[id]; seen {
				continue
			}
			devices[id] = struct{}{}

			if tier, ok := item.Properties().String("tier"); ok {
				tiers[tier] = struct{}{}
			}
		}
	}

	tierList := make([]string, 0, len(tiers))
	for t := range tiers {
		tierList = append(tierList, t)
	}
	sort.Strings(tierList)

	return BlastRadius{
		DeviceCount: len(devices),
		Tiers:       tierList,
	}
}
