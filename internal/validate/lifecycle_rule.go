package validate

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// LifecycleRule is the plan-time half of enforcing PLAN.md Section 11's
// eight-state device lifecycle (IMPLEMENTATION.md Phase W3 names its
// absence explicitly: LifecycleState.CanExecute exists and has zero
// production callers anywhere in the repository). It rejects any task
// whose target resolves to a device that is not Active, the same way
// CapabilityRule rejects a device that lacks a required capability.
//
// This rule owns only the plan-time check. Phase W5 owns the matching
// runtime guard in the executor and Phase 14 owns the dispatcher-side
// check; all three are required for the same defense-in-depth reason
// action_ssh.go re-checks HasCapability after CapabilityRule already did,
// since a runbook can be executed by a path that never called Validate.
//
// Unlike CapabilityRule, this rule applies to every task regardless of
// fqcn: a non-Active device must never accept any real work, not only
// work that happens to declare a capability requirement.
//
// It admits exactly what the executor admits, by asking the same function
// (engine.LifecycleAdmitsIn) with the mode the run will use: a check may
// target a simulate-locked device, and nothing else changes. A rule that
// refused what the executor accepts would make check mode unable to reach
// the one kind of device it exists to reach.
func LifecycleRule(world WorldView) []Finding {
	var findings []Finding

	for id, task := range world.DAG.Nodes {
		_, devices := world.taskDevices(task)
		if len(devices) == 0 {
			continue
		}

		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}

		for _, dev := range devices {
			if ok, _ := engine.LifecycleAdmitsIn(engine.TaskMode(world.Mode, world.DAG, task), dev); !ok {
				findings = append(findings, Finding{
					RuleName: "lifecycle",
					Node:     id,
					Device:   dev.ID(),
					Message: fmt.Sprintf(
						"device %q is %s, not active, and cannot be targeted by task %s",
						dev.Name(), dev.State(), label,
					),
				})
			}
		}
	}

	return findings
}

func init() {
	Register(LifecycleRule)
}
