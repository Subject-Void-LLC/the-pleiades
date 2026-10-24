package validate

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// CapabilityRule refuses a task whose device lacks a capability its action
// requires. For a Collection method that is every capability its manifest
// requires (collection.CheckCapabilities, the check the engine makes again
// before the method runs); for a legacy transport action it is the one
// engine.ActionCapability names. It resolves the task's effective target
// (engine.TaskTarget: Params["target"], falling back to the runbook's own
// Hosts default; a device name or a tag either way) and rejects any
// resolved device that does not declare it, and, for a legacy action, any
// target that resolves to no device at all.
//
// Until Phase 117b only the legacy actions were checked here, so a method
// requiring SystemdCapable validated against a Cisco switch and was refused
// only once the run reached it.
//
// A task with a condition of its own is not held to its capabilities: the
// run may skip it on any device, and the engine checks it on each device
// where its condition holds (engine.DAG.Conditional). A legacy action's
// target that matches nothing is still refused, condition or not.
//
// world.DAG.Nodes is already the flattened view produced by internal/engine
// (every pretasks/tasks/posttasks entry plus every block/rescue/always
// descendant, keyed by its synthesized ID), so this loop needs no recursion
// of its own to reach every task in the runbook.
func CapabilityRule(world WorldView) []Finding {
	var findings []Finding

	for id, task := range world.DAG.Nodes {
		conditional := world.DAG.Conditional(id)
		if desc, ok := collection.Lookup(task.FQCN); ok {
			if conditional {
				continue
			}
			findings = append(findings, methodCapabilityFindings(world, id, task, desc)...)
			continue
		}
		required, ok := engine.ActionCapability[task.FQCN]
		if !ok {
			continue
		}

		target, devices := world.taskDevices(task)
		if target == "" && len(devices) == 0 {
			continue
		}

		label := taskLabel(id, task)

		if len(devices) == 0 {
			findings = append(findings, Finding{
				RuleName: "capability",
				Node:     id,
				Message:  fmt.Sprintf("target %q matches no inventory host or tag (task %s)", target, label),
			})
			continue
		}
		if conditional {
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

// methodCapabilityFindings checks a Collection method's required
// capabilities against each device its task resolves to. A call that runs
// with no device (engine.TaskTarget is empty and nothing resolved) is held
// to the same check with no device, which refuses it when the method
// requires any capability, as the engine would at run time.
func methodCapabilityFindings(world WorldView, id string, task *engine.Task, desc collection.Descriptor) []Finding {
	// The call is held to the shape its parameters select, as the engine
	// holds it (collection.Manifest.For).
	shaped := desc.Manifest.For(task.Params)
	if len(shaped.RequiredCapabilities) == 0 {
		return nil
	}
	target, devices := world.taskDevices(task)
	if target == "" && len(devices) == 0 {
		// The method requires a capability (above), so a call with no
		// device is always refused.
		err := collection.CheckCapabilities(nil, task.FQCN, shaped)
		return []Finding{{RuleName: "capability", Node: id, Message: fmt.Sprintf("%v (task %s)", err, taskLabel(id, task))}}
	}
	var findings []Finding
	for _, dev := range devices {
		if err := collection.CheckCapabilities(dev, task.FQCN, shaped); err != nil {
			findings = append(findings, Finding{
				RuleName: "capability",
				Node:     id,
				Device:   dev.ID(),
				Message:  fmt.Sprintf("%v, so task %s cannot run on it", err, taskLabel(id, task)),
			})
		}
	}
	return findings
}

// taskLabel names a task for a human reading a Finding's Message: its
// synthesized ID alone, or the ID plus its human-given Name when one is
// set. Name has no uniqueness requirement, so it is never used as an
// identity, only as a locator alongside the ID.
func taskLabel(id string, task *engine.Task) string {
	if task.Name != "" {
		return fmt.Sprintf("%s (name %q)", id, task.Name)
	}
	return id
}

func init() {
	Register(CapabilityRule)
}
