// Package inventorytest provides a reusable InventoryItem test double, so
// package tests do not each hand-roll a fake implementing every method of
// the base contract.
package inventorytest

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Stub is a minimal, overridable InventoryItem. The zero value is a valid,
// empty device; set fields before use to shape a specific test case.
type Stub struct {
	StubID    inventory.DeviceID
	StubName  string
	Props     map[string]inventory.PropertyValue
	StubTags  []inventory.Tag
	Caps      []capability.Name
	StubState inventory.LifecycleState
	StubSrc   inventory.SourceAuthority
}

// ID returns the stub's configured DeviceID.
func (s *Stub) ID() inventory.DeviceID { return s.StubID }

// Name returns the stub's configured display name.
func (s *Stub) Name() string { return s.StubName }

// Properties wraps the stub's Props map in the typed accessor.
func (s *Stub) Properties() inventory.Properties { return inventory.NewProperties(s.Props) }

// Tags returns the stub's configured tags.
func (s *Stub) Tags() []inventory.Tag { return s.StubTags }

// HasCapability reports whether name is present in Caps. Unlike the real
// concrete types, this does not also check a structural assertion: a test
// declaring a capability on the stub is asserting the behavior it wants,
// not classifying a real device.
func (s *Stub) HasCapability(name capability.Name) bool {
	for _, c := range s.Caps {
		if c == name {
			return true
		}
	}
	return false
}

// Capabilities returns the stub's configured capability set.
func (s *Stub) Capabilities() []capability.Name { return s.Caps }

// AddInfo sets a property, honoring the overwrite flag like a real item.
func (s *Stub) AddInfo(key string, value inventory.PropertyValue, overwrite bool) error {
	if s.Props == nil {
		s.Props = map[string]inventory.PropertyValue{}
	}
	if _, exists := s.Props[key]; exists && !overwrite {
		return fmt.Errorf("property already exists: %s", key)
	}
	s.Props[key] = value
	return nil
}

// RemoveInfo deletes a property, returning an error if it was not set.
func (s *Stub) RemoveInfo(key string) error {
	if _, exists := s.Props[key]; !exists {
		return fmt.Errorf("property does not exist: %s", key)
	}
	delete(s.Props, key)
	return nil
}

// ShowInfo returns the same view as Properties.
func (s *Stub) ShowInfo() inventory.Properties { return s.Properties() }

// Version always returns 0: the stub does not track revisions.
func (s *Stub) Version() uint64 { return 0 }

// History always returns nil: the stub does not track revisions.
func (s *Stub) History() []inventory.Revision { return nil }

// State returns the stub's configured lifecycle state.
func (s *Stub) State() inventory.LifecycleState { return s.StubState }

// Source returns the stub's configured source authority.
func (s *Stub) Source() inventory.SourceAuthority { return s.StubSrc }
