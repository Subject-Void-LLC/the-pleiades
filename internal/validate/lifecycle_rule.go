package validate

import (
	"fmt"
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
func LifecycleRule(world WorldView) []Finding {
	var findings []Finding

	for id, task := range world.DAG.Nodes {
		target, _ := task.Params["target"].(string)
		if target == "" {
			continue
		}

		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}

		for _, dev := range world.Resolve(target) {
			if !dev.State().CanExecute() {
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
