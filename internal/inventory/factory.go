// Package inventory hydrates storage-agnostic Records into the concrete
// device types that satisfy the base InventoryItem contract (pkg/inventory),
// and adapts them to and from whichever repository backs the platform at a
// given tier (ent-backed at Walk and above, YAML-backed at Crawl).
package inventory

import (
	"fmt"
	"maps"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
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
		return nil, fmt.Errorf("record has no device type: %q", rec.Name)
	}

	constructor, exists := f.registry[rec.Type]
	if !exists {
		return nil, fmt.Errorf("unsupported device type: %s", rec.Type)
	}

	item, err := constructor(rec)
	if err != nil {
		return nil, err
	}
	if err := checkSSHLegacy(item); err != nil {
		return nil, fmt.Errorf("device %q: %w", rec.Name, err)
	}
	return item, nil
}

// checkSSHLegacy refuses a record's legacy SSH flags (pkg/remoteexec's
// legacy.go) when one is not a boolean, or when one is true on a device no
// SSH connection is ever made to, where it allows nothing: a record that
// says a weakening is on where none can be is one whose author believes
// something the platform does not do. A device reached over SSH, or through
// an SSH console server (a CML controller), may set them; whether a given
// connection needed one is said on that connection, since one device can run
// two SSH servers (remoteexec.Conn.LegacyWarnings).
func checkSSHLegacy(item inventory.InventoryItem) error {
	// A constructor that built nothing has no record to hold to anything.
	if item == nil {
		return nil
	}
	legacy, err := remoteexec.ParseLegacy(item.Properties())
	if err != nil {
		return err
	}
	if legacy.IsZero() {
		return nil
	}
	_, ssh := item.(capability.SSHTransportCapable)
	_, console := item.(capability.CMLAPICapable)
	if ssh || console {
		return nil
	}
	for _, key := range remoteexec.LegacyProperties() {
		if on, _ := item.Properties().Raw()[key].(bool); on {
			return fmt.Errorf("property %s is true and this device type is not reached over SSH, so it allows nothing: remove it", key)
		}
	}
	return nil
}

// Rebuild constructs item afresh through f from its current type,
// properties, tags and state, and returns the constructor's error.
// AddInfo changes a property without the item's type seeing it, so a
// caller that edits an item calls this before saving it: a value the type
// would refuse on the next load is refused now, before it is written.
func (f *ItemFactory) Rebuild(item inventory.InventoryItem) error {
	_, err := f.Rebuilt(item)
	return err
}

// Rebuilt is Rebuild returning the item built afresh from item's current
// record, for a caller that must ask the rebuilt item a question (whether
// a capability a grant adds is now held) before it saves item. The
// returned item carries no history; save item, not it.
func (f *ItemFactory) Rebuilt(item inventory.InventoryItem) (inventory.InventoryItem, error) {
	deviceType, err := itemDeviceType(item)
	if err != nil {
		return nil, err
	}
	return f.Build(record.Record{
		ID: item.ID(), Name: item.Name(), Type: deviceType,
		Properties: item.Properties().Raw(), Tags: item.Tags(), State: item.State(),
	})
}
