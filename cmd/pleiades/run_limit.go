// --limit on the CLI: the devices of a run narrowed by engine.ParseLimit's
// pattern, the one matcher a Controller's job reads its limit with too.
package main

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// limitResolver answers targets as inner does, keeping, among the runbook's
// hosts, only the devices the limit admits by name or tag (a tag is what the
// CLI groups by). A device outside the hosts (a lab server or a Batfish
// service a task names) is not limited, as Ansible's --limit does not limit
// delegate_to, and as serial: does not batch it.
type limitResolver struct {
	inner validate.WorldView
	hosts map[inventory.DeviceID]bool
	limit engine.Limit
}

// Resolve implements engine.TargetResolver.
func (l limitResolver) Resolve(target string) []inventory.InventoryItem {
	var out []inventory.InventoryItem
	for _, item := range l.inner.Resolve(target) {
		if !l.hosts[item.ID()] || l.limit.Admits(item.Name(), tagNames(item)) {
			out = append(out, item)
		}
	}
	return out
}

// tagNames returns item's tags as strings.
func tagNames(item inventory.InventoryItem) []string {
	tags := item.Tags()
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = string(t)
	}
	return out
}

// applyLimit returns world with the runbook's hosts narrowed to limit,
// refusing a limit that leaves none of them: a typo in --limit must not run nothing and
// call it success. No limit returns world unchanged.
func applyLimit(world validate.WorldView, dag *engine.DAG, limit engine.Limit, text string) (validate.WorldView, error) {
	if limit.IsZero() {
		return world, nil
	}
	all, _ := engine.ResolveTargets(world, dag.Hosts)
	hosts := map[inventory.DeviceID]bool{}
	for _, item := range all {
		hosts[item.ID()] = true
	}
	limited := world
	limited.Resolver = limitResolver{inner: world, hosts: hosts, limit: limit}
	if kept, _ := engine.ResolveTargets(limited, dag.Hosts); len(all) > 0 && len(kept) == 0 {
		return world, fmt.Errorf("--limit %q matches none of the runbook's hosts (%s)", text, dag.Hosts)
	}
	return limited, nil
}
