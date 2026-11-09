package inventory

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// checkNewDevice is what both stores require of a device before they create
// it, and returns the type Create stores.
//
// Its name must be able to be its address. A device is addressed by name in
// the API and the UI, so "", "." and ".." (recordname) would be a record
// stored but never opened, changed or deleted by its URL. Create is the one
// place a device's name is chosen (Save never changes it), so refusing here
// covers every caller: the API, the UI, a sync, an import. The error
// matches recordname.ErrUnaddressable and says why.
func checkNewDevice(item inventory.InventoryItem) (string, error) {
	if err := recordname.Check(item.Name()); err != nil {
		return "", fmt.Errorf("creating device: %w", err)
	}
	return itemDeviceType(item)
}

// typed is satisfied by any item that can report the registry key it was
// hydrated through, which Create needs in order to store a new row's
// immutable type column. It is kept out of inventory.InventoryItem for the
// same reason versioned is: the public SDK contract describes what a device
// IS, and the string key its constructor is registered under is a
// persistence concern, not part of the device's identity.
type typed interface {
	DeviceType() string
}

// itemDeviceType extracts the device type Create must store, rejecting an
// item that cannot report one. Failing here rather than inserting a row
// with an empty type is the difference between a loud error at write time
// and a stored row that no factory can ever hydrate: the type column is
// immutable, so there is no later write that could repair it.
func itemDeviceType(item inventory.InventoryItem) (string, error) {
	t, ok := item.(typed)
	if !ok {
		return "", fmt.Errorf("item %s does not report a device type, refusing to create a row nothing can hydrate", item.Name())
	}
	deviceType := t.DeviceType()
	if deviceType == "" {
		return "", fmt.Errorf("item %s has an empty device type, refusing to create a row nothing can hydrate", item.Name())
	}
	return deviceType, nil
}
