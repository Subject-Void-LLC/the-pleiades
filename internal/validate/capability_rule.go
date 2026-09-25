package validate

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// CapabilityRule is the first production caller of InventoryItem's
// HasCapability anywhere in this repository (IMPLEMENTATION.md Phase W3
// names its absence explicitly). For every task whose fqcn requires a
// capability, it resolves the task's effective target (engine.TaskTarget:
// Params["target"], falling back to the runbook's own Hosts default; a
// device name or a tag either way) and rejects any resolved device that
// does not declare it, and any target that resolves to no device at all.
//
// world.DAG.Nodes is already the flattened view produced by internal/engine
// (every pretasks/tasks/posttasks entry plus every block/rescue/always
// descendant, keyed by its synthesized ID), so this loop needs no recursion
// of its own to reach every task in the runbook.
func CapabilityRule(world WorldView) []Finding {
	var findings []Finding

	for id, task := range world.DAG.Nodes {
		required, ok := engine.ActionCapability[task.FQCN]
		if !ok {
			continue
		}

		target, devices := world.taskDevices(task)
		if target == "" && len(devices) == 0 {
			continue
		}

		// label names the task for a human reading a Finding's Message: its
		// synthesized ID alone, or the ID plus its human-given Name when
		// one is set. Name has no uniqueness requirement, so it is never
		// used as an identity, only as a locator alongside the ID.
		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}

		if len(devices) == 0 {
			findings = append(findings, Finding{
				RuleName: "capability",
				Node:     id,
				Message:  fmt.Sprintf("target %q matches no inventory host or tag (task %s)", target, label),
			})
			continue
		}

		for _, dev := range devices {
			if !dev.HasCapability(required) {
				findings = append(findings, Finding{
					RuleName: "capability",
					Node:     id,
					Device:   dev.ID(),
					Message: fmt.Sprintf(
						"device %q does not have capability %s, required by task %s",
						dev.Name(), required, label,
					),
				})
			}
		}
	}

	return findings
}

func init() {
	Register(CapabilityRule)
}
