// Package inventory provides the core inventory item types and interfaces.
//
// This package defines the base contract for all devices managed by the platform,
// ensuring strict type safety before tasks are dispatched.
package inventory

// InventoryItem defines the base contract that all inventory items must satisfy.
// Every concrete type implements this interface to participate in the mesh.
type InventoryItem interface {
	// ID returns the globally unique identifier for the device.
	ID() string

	// Properties returns the raw key-value attributes associated with the device.
	Properties() map[string]interface{}

	// Tags returns the classification labels for dynamic grouping.
	Tags() []string

	// HasCapability acts as the polymorphic guard. It returns true if the device
	// implements the requested capability interface.
	HasCapability(capName string) bool
}
