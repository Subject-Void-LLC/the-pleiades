package native

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// wireDevice is this package's name for the device adapter a dispatch
// payload becomes: pkg/external.Device, which rebuilds a full
// inventory.InventoryItem from what the wire carries.
//
// The adapter used to live here. It moved to pkg/external when external
// Collections arrived, because a program built outside this repository has
// to receive the identical device type the Runner's own per-task child
// does, and a program outside the repository can import pkg/ and nothing
// else. The alias keeps this package's own name for it, and the
// trusted-relay reasoning behind HasCapability and State is on that type.
type wireDevice = external.Device

// newWireDevice builds the InventoryItem adapter for payload.
func newWireDevice(payload wire.DispatchPayload) *wireDevice {
	return external.NewDevice(payload)
}
