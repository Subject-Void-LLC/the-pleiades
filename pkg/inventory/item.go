// Package inventory defines the plugin-facing contracts for The Pleiades
// inventory system: item identity, lifecycle, versioning, and the typed
// property accessor every concrete device type builds on.
//
// This package is part of the public SDK (pkg/). It must never import
// internal/, so third-party device types and sync plugins can compile
// against it without pulling in storage or transport internals.
package inventory

import (
	"fmt"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// DeviceID is the stable, opaque identifier for an inventory item. It is
// never the mutable display name, so renaming a device never breaks a
// lock key, a fact key, or a pagination cursor that references it.
type DeviceID string

// Tag is a classification label attached to an item for dynamic grouping.
type Tag string

// PropertyValue is the value type stored in a device's property bag. It is
// deliberately untyped: raw device properties come from heterogeneous
// sources (YAML scalars, JSONB columns), and forcing one Go type here
// would just move the type assertion into every caller instead of
// removing it. Properties (properties.go) is the checked accessor that
// keeps that assertion from ever panicking.
type PropertyValue = any

// LifecycleState is the eight-state lifecycle every inventory item moves
// through, from first discovery to archival (PLAN.md Section 11).
type LifecycleState uint8

// The eight lifecycle states, in the order PLAN.md Section 11 defines them.
const (
	StateDiscovered LifecycleState = iota
	StateQuarantined
	StateOnboarding
	StateActive
	StateSimulateLocked
	StateUnreachable
	StateDecommissioning
	StateArchived
)

// String renders the state for log lines and validation messages.
func (s LifecycleState) String() string {
	switch s {
	case StateDiscovered:
		return "discovered"
	case StateQuarantined:
		return "quarantined"
	case StateOnboarding:
		return "onboarding"
	case StateActive:
		return "active"
	case StateSimulateLocked:
		return "simulate-locked"
	case StateUnreachable:
		return "unreachable"
	case StateDecommissioning:
		return "decommissioning"
	case StateArchived:
		return "archived"
	default:
		return "unknown"
	}
}

// ParseLifecycleState is the inverse of String, for adapters hydrating a
// stored state back into the domain. It deliberately returns an error on an
// unrecognized value rather than defaulting to StateActive: a state this
// build does not know about must never be silently promoted into the one
// state that permits execution.
func ParseLifecycleState(s string) (LifecycleState, error) {
	switch s {
	case "discovered":
		return StateDiscovered, nil
	case "quarantined":
		return StateQuarantined, nil
	case "onboarding":
		return StateOnboarding, nil
	case "active":
		return StateActive, nil
	case "simulate-locked":
		return StateSimulateLocked, nil
	case "unreachable":
		return StateUnreachable, nil
	case "decommissioning":
		return StateDecommissioning, nil
	case "archived":
		return StateArchived, nil
	default:
		return StateQuarantined, fmt.Errorf("unrecognized lifecycle state: %q", s)
	}
}

// CanExecute reports whether a runbook may target an item in this state.
// Only StateActive accepts real work; every other state is a deliberate
// hold (quarantine, an incomplete onboarding, or an archival state).
func (s LifecycleState) CanExecute() bool {
	return s == StateActive
}

// Revision is one entry in an item's audit trail, recorded by AddInfo and
// RemoveInfo so History can answer "why is this value what it is."
type Revision struct {
	Version   uint64
	ChangedAt time.Time
	Field     string
	OldValue  PropertyValue
	NewValue  PropertyValue
}

// SourceAuthority names the plugin that owns a device's data and when it
// last synced. Section 11 requires exactly one authoritative source per
// item; reconciliation on re-sync compares against this value.
type SourceAuthority struct {
	Plugin   string
	SyncedAt time.Time
}

// InventoryItem is the base contract every concrete device type satisfies.
// Section 1 makes versioning, lifecycle, and source authority base-level
// so no concrete type has to retrofit them later.
type InventoryItem interface {
	// ID returns the stable, opaque identifier for this item.
	ID() DeviceID

	// Name returns the mutable, human-readable display name.
	Name() string

	// Properties returns the typed accessor over this item's raw metadata.
	Properties() Properties

	// Tags returns the classification labels used for dynamic grouping.
	Tags() []Tag

	// HasCapability is the polymorphic guard, checked before dispatch. A
	// true result guarantees the matching type assertion succeeds.
	HasCapability(name capability.Name) bool

	// Capabilities lists every capability this item currently declares.
	Capabilities() []capability.Name

	// AddInfo adds or overwrites a property, recording a Revision. It
	// returns an error if the key already exists and overwrite is false.
	AddInfo(key string, value PropertyValue, overwrite bool) error

	// RemoveInfo removes a property, recording a Revision. It returns an
	// error if the key does not exist.
	RemoveInfo(key string) error

	// ShowInfo returns every property currently set on the item.
	ShowInfo() Properties

	// Version returns the current revision number.
	Version() uint64

	// History returns the full audit trail, oldest first.
	History() []Revision

	// State returns the item's current lifecycle state.
	State() LifecycleState

	// Source returns the plugin that authoritatively owns this item.
	Source() SourceAuthority
}
