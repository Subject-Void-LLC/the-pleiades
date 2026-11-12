// Command pleiades: the warnings validate gives about the grants of the
// devices a runbook reaches.
package main

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// grantRuleName is the rule name these warnings print under.
const grantRuleName = "grants"

// grantWarnings is advice about the grants of the devices world's runbook
// reaches: a grant no loaded program defines (its program was removed, or
// its approval revoked), which grants nothing; and a grant that counts only
// with a warning (one made before grants carried their definition's digest,
// on a release line where that still counts). A grant that lapsed for any
// other reason is left to the capability check of a task that needs it,
// which names the reason (collection.CheckCapabilities), so it is not said
// twice. Warnings never fail validation.
func grantWarnings(world validate.WorldView) []validate.Finding {
	var out []validate.Finding
	for _, device := range validate.CalculateBlastRadius(world).Devices {
		noter, ok := device.(inventory.GrantNoter)
		if !ok {
			continue
		}
		for _, n := range noter.GrantNotes() {
			_, defined := capability.DataOf(n.Capability)
			if n.Lapsed && defined {
				continue
			}
			out = append(out, validate.Finding{RuleName: grantRuleName, Device: device.ID(), Message: n.Reason})
		}
	}
	return out
}
