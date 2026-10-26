// UnmatchedTargetWarnings: the advice that a task's hosts or target names no
// device or tag in this inventory.
package validate

import (
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// UnmatchedTargetWarnings warns, once per name, of a target a method's task
// names (its target:, or the runbook's hosts:) that matches no device and no
// tag in world's inventory, which the run then fails on. It is a warning,
// not a finding, because the devices may be added before the run (an
// import-cml, an onboarding), which a runbook written ahead of them expects.
// A legacy transport action's unmatched target is CapabilityRule's finding
// instead, and a call that needs no device is never held to one. A task with
// a condition of its own is left out, as CapabilityRule leaves it: the run
// may skip it.
func UnmatchedTargetWarnings(world WorldView) []Finding {
	unmatched := map[string][]string{}
	for id, task := range world.DAG.Nodes {
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || desc.Manifest.ExecutionContext.Device == collection.DeviceNone || world.DAG.Conditional(id) {
			continue
		}
		// A target rendered from data (within:) is not known until the run.
		target := engine.TaskTarget(world.DAG, task)
		if task.Within != "" || target == "" || len(world.Resolve(target)) > 0 {
			continue
		}
		unmatched[target] = append(unmatched[target], id)
	}
	targets := make([]string, 0, len(unmatched))
	for t := range unmatched {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	var out []Finding
	for _, t := range targets {
		nodes := unmatched[t]
		sort.Strings(nodes)
		out = append(out, Finding{
			RuleName: "target",
			Node:     nodes[0],
			Message:  fmt.Sprintf("%q matches no device or tag in this inventory (%d task(s), first %s); the run fails there unless it is added first", t, len(nodes), nodes[0]),
		})
	}
	return out
}
