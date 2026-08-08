// Package inventory hydrates storage-agnostic Records into the concrete
// device types that satisfy the base InventoryItem contract (pkg/inventory),
// and adapts them to and from whichever repository backs the platform at a
// given tier (ent-backed at Crawl and above, YAML-backed at Walk).
package inventory

import (
	"fmt"
	"maps"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// ItemFactory hydrates Records into strongly typed InventoryItem values,
// keyed by a device-type registry so new types can be added without
// editing Build (Registry pattern, Section 25: build once, reuse always).
type ItemFactory struct {
	registry map[string]record.Constructor
}

// NewItemFactory initializes a factory from every device type currently
// registered in the shared record.Types registry (record/types_registry.go).
// It takes no arguments and always returns the batteries-included factory:
// every existing caller depends on this exact zero-arg signature. Built-in
// device types (devices/cisco, devices/linux) register themselves via their
// own init(), triggered by builtins.go's blank imports; this function never
// names either package, so a new in-tree device type is added by writing
// its package and one blank import in builtins.go, never by editing this
// function. Callers who want a custom subset of constructors instead of the
// registry's full contents should use NewItemFactoryWithConstructors.
func NewItemFactory() *ItemFactory {
	return NewItemFactoryWithConstructors(record.AllTypes())
}

// NewItemFactoryWithConstructors initializes a factory with exactly the
// given device-type constructors, for callers who want a custom subset
// instead of the registry's full batteries-included set (for example, a
// test proving a factory scoped to one fake type does not also see the
// real built-in types).
func NewItemFactoryWithConstructors(entries map[string]record.Constructor) *ItemFactory {
	return &ItemFactory{registry: maps.Clone(entries)}
}

// Build hydrates a Record into a concrete InventoryItem based on its Type
// field. It never receives or constructs an ORM row: Type, Properties, and
// every other field are extracted by the calling repository adapter, so
// this function has no idea whether the Record came from Postgres, SQLite,
// or a YAML file.
func (f *ItemFactory) Build(rec record.Record) (inventory.InventoryItem, error) {
	if rec.Type == "" {
		return nil, fmt.Errorf("record has no device type: %s", rec.Name)
	}

	constructor, exists := f.registry[rec.Type]
	if !exists {
		return nil, fmt.Errorf("unsupported device type: %s", rec.Type)
	}

	return constructor(rec)
}
