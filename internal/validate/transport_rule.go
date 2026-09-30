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
// The check itself is collection.CheckTransports, the one the engine
// makes again before the method runs and the generic dispatchers make for
// the concrete method they pick, so plan time and run time answer the
// same question the same way. Devices come from taskDevices, so a call
// that needs no device (engine.TaskTarget) has none to check.
func TransportRule(world WorldView) []Finding {
	var findings []Finding

	for id, task := range world.DAG.Nodes {
		desc, ok := collection.Lookup(task.FQCN)
		if !ok || len(desc.Manifest.SupportedTransports) == 0 {
			continue
		}
		_, devices := world.taskDevices(task)

		label := id
		if task.Name != "" {
			label = fmt.Sprintf("%s (name %q)", id, task.Name)
		}

		for _, dev := range devices {
			if err := collection.CheckTransports(dev, task.FQCN, desc.Manifest); err != nil {
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
