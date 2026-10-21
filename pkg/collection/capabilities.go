// A Collection method's required capabilities, held to its device.
package collection

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// CheckCapabilities refuses a device that lacks a capability the method
// named fqcn requires (Manifest.RequiredCapabilities), and a call with no
// device at all when the method requires any.
//
// It uses HasCapability rather than comparing declared names, for two
// reasons. HasCapability resolves the hierarchy (record.Base.Declares runs
// capability.Resolves), so a device declaring SystemdCapable satisfies a
// method requiring ServiceManagerCapable, which is what the parent exists
// for. And on a real device type it also runs the structural assertion, so
// a declaration the Go type cannot back does not pass.
//
// It is the one check the plan-time rule (internal/validate's
// CapabilityRule), the run-time gate (internal/engine) and the Controller's
// admission of a job (internal/dispatch) all make, beside CheckTransports.
// Until Phase 117b only the run-time gate made it, so a method requiring
// SystemdCapable validated against a Cisco switch and was refused only once
// the run reached it.
func CheckCapabilities(device inventory.InventoryItem, fqcn string, m Manifest) error {
	if len(m.RequiredCapabilities) == 0 {
		return nil
	}
	if device == nil {
		return fmt.Errorf("collection method %q requires capabilities %v but the task has no target device",
			fqcn, m.RequiredCapabilities)
	}
	for _, required := range m.RequiredCapabilities {
		if !device.HasCapability(required) {
			return fmt.Errorf("collection method %q requires capability %s, which device %q does not have",
				fqcn, required, device.Name())
		}
	}
	return nil
}
