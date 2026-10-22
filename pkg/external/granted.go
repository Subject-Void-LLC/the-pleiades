// Package external: what a method reads of a capability its program
// defines.
package external

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Granted returns the properties of device's grant of name, a capability
// this program defines (Program.Capabilities), and whether device holds
// one. The Pleiades sends a program its own capabilities' grants only, and
// only ones the device holds: a grant whose properties no longer fit the
// definition, or whose device lacks a compiled capability it requires, is
// not sent. The engine refuses a method requiring a capability the device
// does not hold before the method runs, so a method reaching here for one
// it requires finds it.
func Granted(device inventory.InventoryItem, name capability.Name) (map[string]any, bool) {
	if device == nil {
		return nil, false
	}
	grants, _, err := inventory.GrantsFrom(device.Properties())
	if err != nil {
		return nil, false
	}
	for _, g := range grants {
		if g.Capability == name {
			return g.Properties, true
		}
	}
	return nil, false
}
