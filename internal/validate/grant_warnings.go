// Package validate: the warnings every validation gives about the grants
// of the capabilities external programs define, on the devices a runbook
// reaches.
package validate

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// GrantRuleName is the rule name a grant warning carries, so a caller that
// shows only some of a report's warnings (a run's, a Runner's job log) can
// find these.
const GrantRuleName = "grants"

// GrantWarnings is advice about the grants recorded on each device world's
// runbook reaches (inventory.GrantNoter's notes): a grant no loaded program
// defines, which grants nothing (its program was removed, or its approval
// revoked); a grant that lapsed for any other reason (a changed definition,
// a repointed device, a time that cannot be read); and one that counts only
// with a warning (made before grants carried their definition's digest, on
// a release line where that still counts). Each names the device, the
// capability and what to do. A lapse a task on that device is held to is
// left out, since CapabilityRule's finding already quotes the same reason
// as an error, and saying it twice is noise. Warnings never fail
// validation.
//
// It reads what this process holds: the definitions its loaded programs
// gave. The command line and a Runner load programs; a Controller loads
// none, never validates a runbook, and so never gives these, since to it
// every grant would read as defined by no program.
func GrantWarnings(world WorldView) []Finding {
	if world.DAG == nil {
		return nil
	}
	refused := capabilityRuleRequirements(world)
	var out []Finding
	for _, device := range CalculateBlastRadius(world).Devices {
		noter, ok := device.(inventory.GrantNoter)
		if !ok {
			continue
		}
		for _, n := range noter.GrantNotes() {
			if n.Lapsed && requiredOf(refused[device.ID()], n.Capability) {
				continue
			}
			out = append(out, Finding{RuleName: GrantRuleName, Device: device.ID(), Message: n.Reason})
		}
	}
	return out
}

// capabilityRuleRequirements is, per device, each manifest (in the shape
// its task's parameters select) of a Collection method that a task with no
// condition of its own holds that device to, as CapabilityRule does.
func capabilityRuleRequirements(world WorldView) map[inventory.DeviceID][]collection.Manifest {
	out := map[inventory.DeviceID][]collection.Manifest{}
	for id, task := range world.DAG.Nodes {
		if world.DAG.Conditional(id) {
			continue
		}
		desc, ok := collection.Lookup(task.FQCN)
		if !ok {
			continue
		}
		shaped := desc.Manifest.For(task.Params)
		_, devices := world.taskDevices(task)
		for _, dev := range devices {
			out[dev.ID()] = append(out[dev.ID()], shaped)
		}
	}
	return out
}

// requiredOf reports whether any of manifests requires name of every call,
// through Manifest.Requires, so an alternative is read the way the
// admission check reads it.
func requiredOf(manifests []collection.Manifest, name capability.Name) bool {
	for _, m := range manifests {
		if m.Requires(name) {
			return true
		}
	}
	return false
}
