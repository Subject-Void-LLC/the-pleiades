package native

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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

// dispatchedDevice builds the device a dispatch names as its real type
// (record.LookupType) from the type and accessor properties the Controller
// sent, so every accessor answers here as it does on the Controller. A
// payload naming no type, or one this Runner does not know (an older
// Controller, or a type newer than this binary), gets the address-only
// device, as every dispatch did before. A known type whose record will not
// build is an error: the two binaries disagree about the device, and
// running against a different device than the Controller admitted is worse
// than not running.
func dispatchedDevice(payload wire.DispatchPayload) (inventory.InventoryItem, error) {
	if payload.DeviceType == "" {
		return newWireDevice(payload), nil
	}
	ctor, known := record.LookupType(payload.DeviceType)
	if !known {
		return newWireDevice(payload), nil
	}
	props := make(map[string]inventory.PropertyValue, len(payload.DeviceProperties))
	for k, v := range payload.DeviceProperties {
		props[k] = v
	}
	tags := make([]inventory.Tag, 0, len(payload.Tags))
	for _, tag := range payload.Tags {
		tags = append(tags, inventory.Tag(tag))
	}
	item, err := ctor(record.Record{
		ID:         inventory.DeviceID(payload.DeviceID),
		Name:       payload.DeviceName,
		Type:       payload.DeviceType,
		Properties: props,
		Tags:       tags,
		// The Controller admitted the device before dispatching it, as
		// the address-only device has always reported.
		State:        inventory.StateActive,
		Capabilities: payload.Capabilities,
	})
	if err != nil {
		return nil, fmt.Errorf("rebuilding device %q as a %s: %w", payload.DeviceName, payload.DeviceType, err)
	}
	return item, nil
}
