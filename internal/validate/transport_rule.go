// TransportRule: the plan-time check that a task's device can be reached
// over a transport its Collection method uses.
package validate

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TransportRule refuses a task whose Collection method cannot reach one
// of its devices: the method declares the transports it reaches a device
// over (Manifest.SupportedTransports), and the device reaches none of
// them. PLAN.md 14.4 has always said methods declare transports and the
// system validates them at plan time; until Phase 75 nothing read the
// declaration, so an SSH-only method validated against a WinRM-only
// Windows server and failed at run time.
//
// The check itself is collection.CheckTransports, the transport half of
// collection.Admits, which the engine makes again before the method runs,
// and the one the generic dispatchers make for the concrete method they
// pick, so plan time and run time answer the same question the same way.
// CapabilityRule makes the rest of Admits and leaves its transport refusals
// to this rule, so a device lacking both a capability and a transport is
// named for each. Devices come from taskDevices, so a call
// that needs no device (engine.TaskTarget) has none to check. A task with
// a condition of its own is left to the engine (engine.DAG.Conditional).
func TransportRule(world WorldView) []Finding {
	var findings []Finding

	for id, task := range world.DAG.Nodes {
		if world.DAG.Conditional(id) {
			continue
		}
		desc, ok := collection.Lookup(task.FQCN)
		if !ok {
			continue
		}
		// The shape the call's parameters select (collection.Manifest.For).
		shaped := desc.Manifest.For(task.Params)
		if len(shaped.SupportedTransports) == 0 {
			continue
		}
		_, devices := world.taskDevices(task)

		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}

		for _, dev := range devices {
			if err := collection.CheckTransports(dev, task.FQCN, shaped); err != nil {
				findings = append(findings, Finding{
					RuleName: "transport",
					Node:     id,
					Device:   dev.ID(),
					Message:  fmt.Sprintf("%v, so task %s cannot run on it", err, label),
				})
			}
		}
	}

	return findings
}

func init() {
	Register(TransportRule)
}
