// Package record is the leaf dependency shared by every vendor device
// package and by internal/inventory's factory. It exists specifically to
// break what would otherwise be an import cycle: vendor device packages
// (internal/inventory/devices/cisco, internal/inventory/devices/linux, and
// future additions) need the storage-agnostic Record DTO and the shared
// Base device state, and internal/inventory/factory.go needs to import
// those same vendor packages to register their constructors. If Record and
// Base lived in internal/inventory itself, the vendor packages would have
// to import internal/inventory to get them, while internal/inventory
// simultaneously imports the vendor packages to register their
// constructors -- a cycle. Hoisting Record and Base into this standalone
// leaf package (which imports only pkg/inventory and pkg/capability, never
// internal/inventory or its subpackages) lets both sides depend downward on
// it instead of on each other.
package record

import (
	"fmt"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// Record is the storage-agnostic input to ItemFactory.Build. Each
// repository adapter (ent-backed, YAML-backed) produces one of these
// instead of passing its own row type into the domain layer, so the
// factory never depends on where a device's data actually lives.
type Record struct {
	// ID is the stable opaque identifier. Adapters are responsible for
	// deriving it from whatever their backend uses as a primary key.
	ID inventory.DeviceID

	// Name is the mutable, human-readable display name.
	Name string

	// Type is the classification key the factory registry is keyed on
	// (e.g. "cisco_router"). It is a first-class field here so Build
	// never has to know where the type was stored (a JSONB column, a
	// YAML field, whatever the adapter's backend looks like).
	Type string

	// Properties holds the raw, adapter-supplied metadata for the item.
	Properties map[string]inventory.PropertyValue

	// Tags carries classification labels forward from the adapter.
	Tags []inventory.Tag

	// State is the item's lifecycle state as known to the adapter.
	State inventory.LifecycleState

	// Source names the adapter's plugin identity for provenance.
	Source inventory.SourceAuthority

	// Version is the item's stored revision number. Carrying it through
	// hydration is what makes it an optimistic-concurrency token rather
	// than a counter that resets to zero every time the item is loaded.
	Version uint64

	// History is the stored audit trail, oldest first. It may be empty
	// when an adapter loads an item without its revisions (a list view,
	// for instance); an empty History is therefore "not loaded", never
	// "this item has never changed". Version is the authority on whether
	// an item has changed.
	History []inventory.Revision

	// Capabilities is the classification-derived capability set (Phase
	// 32's capability granularity decision: a device's capability set is
	// data, walked from internal/classification's rule tree at
	// classification time, not a literal baked into a vendor
	// constructor). It is nil for a Record resolved from an explicit Type
	// with no Classify path, since there is no rule tree to walk in that
	// case; NewBase's caller decides what, if anything, fills that gap.
	Capabilities []capability.Name
}

// Base implements the Section 1 base contract shared by every concrete
// device type: identity, properties, capability declaration, versioning,
// lifecycle, and source authority. Concrete vendor types embed *Base and
// add their own HasCapability (so the outer pointer type is available to
// capability.Implements) plus type-specific accessors.
type Base struct {
	mu      sync.RWMutex // guards props/history/version against concurrent AddInfo/RemoveInfo
	id      inventory.DeviceID
	name    string
	props   map[string]inventory.PropertyValue
	tags    []inventory.Tag
	caps    map[capability.Name]struct{}
	version uint64
	history []inventory.Revision
	state   inventory.LifecycleState
	source  inventory.SourceAuthority

	// deviceType is the registry key this item was hydrated through. It is
	// carried but never mutated: the ent schema's type column is Immutable
	// and the domain has no reclassification operation, so this is a record
	// of how the item was built, not a setting. It exists so a repository
	// writing a brand new row can name the type it is storing; nothing in
	// the public InventoryItem contract exposes it, for the same reason
	// BaseVersion is kept out of that contract.
	deviceType string

	// baseVersion is the version this Base was hydrated at, fixed for the
	// lifetime of the object. version moves as properties change;
	// baseVersion does not, so a repository can still name the row state
	// it read even after several in-memory mutations.
	baseVersion uint64
}

// NewBase copies the Record's data into a fresh Base so later mutation of
// the item never reaches back into the Record it was built from. It
// returns a pointer, and concrete types embed that pointer rather than a
// value, so the embedded sync.RWMutex is never copied.
func NewBase(rec Record, caps []capability.Name) *Base {
	props := make(map[string]inventory.PropertyValue, len(rec.Properties))
	for k, v := range rec.Properties {
		props[k] = v
	}

	capSet := make(map[capability.Name]struct{}, len(caps))
	for _, c := range caps {
		capSet[c] = struct{}{}
	}

	return &Base{
		id:          rec.ID,
		name:        rec.Name,
		deviceType:  rec.Type,
		props:       props,
		tags:        append([]inventory.Tag(nil), rec.Tags...),
		caps:        capSet,
		state:       rec.State,
		source:      rec.Source,
		version:     rec.Version,
		baseVersion: rec.Version,
		history:     append([]inventory.Revision(nil), rec.History...),
	}
}

// BaseVersion reports the version a Base was hydrated at, before any
// in-memory mutation. A repository needs it to build the conditional
// predicate for an optimistic-concurrency write: the update must apply
// only if the stored row is still at this version. Version() reports the
// current (post-mutation) value instead, which is what gets written.
func (b *Base) BaseVersion() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.baseVersion
}

// DeviceType reports the registry key this item was hydrated through. A
// repository needs it to insert a new row, since the classification a
// device is created with is immutable and there is no later write that
// could supply it. It is not part of inventory.InventoryItem: what a device
// IS, is expressed by its capabilities, and the string key its constructor
// happens to be registered under is a persistence detail no third-party
// device type should have to expose.
func (b *Base) DeviceType() string { return b.deviceType }

func (b *Base) ID() inventory.DeviceID { return b.id }

func (b *Base) Name() string { return b.name }

func (b *Base) Properties() inventory.Properties {
	b.mu.RLock()
	defer b.mu.RUnlock()
	// Copy so a caller mutating the returned Properties' backing map (via
	// Raw()) cannot reach back into this device's live state.
	snapshot := make(map[string]inventory.PropertyValue, len(b.props))
	for k, v := range b.props {
		snapshot[k] = v
	}
	return inventory.NewProperties(snapshot)
}

func (b *Base) Tags() []inventory.Tag {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]inventory.Tag(nil), b.tags...)
}

// Declares reports whether this device's classification assigned it name,
// directly or through the Section 8 capability hierarchy (a device that
// only declared AptCapable also Declares the broader PackageManagerCapable
// it descends from -- see capability.Resolves). A concrete type's
// HasCapability ANDs this with capability.Implements, so a device can only
// advertise a capability it both claims and structurally satisfies (the
// CODE_SCAFFOLD binding rule).
func (b *Base) Declares(name capability.Name) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return capability.Resolves(b.caps, name)
}

func (b *Base) Capabilities() []capability.Name {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]capability.Name, 0, len(b.caps))
	for c := range b.caps {
		names = append(names, c)
	}
	return names
}

func (b *Base) AddInfo(key string, value inventory.PropertyValue, overwrite bool) error {
	if inventory.IsReservedProperty(key) {
		return fmt.Errorf("property %s is written only by onboarding", key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	old, exists := b.props[key]
	if exists && !overwrite {
		return fmt.Errorf("property already exists: %s", key)
	}

	b.props[key] = value
	b.version++
	b.history = append(b.history, inventory.Revision{
		Version:   b.version,
		ChangedAt: time.Now().UTC(),
		Field:     key,
		OldValue:  old,
		NewValue:  value,
	})
	return nil
}

func (b *Base) RemoveInfo(key string) error {
	if inventory.IsReservedProperty(key) {
		return fmt.Errorf("property %s is written only by onboarding", key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	old, exists := b.props[key]
	if !exists {
		return fmt.Errorf("property does not exist: %s", key)
	}

	delete(b.props, key)
	b.version++
	b.history = append(b.history, inventory.Revision{
		Version:   b.version,
		ChangedAt: time.Now().UTC(),
		Field:     key,
		OldValue:  old,
		NewValue:  nil,
	})
	return nil
}

// RecordDiscovery writes what onboarding proved into
// inventory.DiscoveredProperty, with a revision like any other change. It
// is the only way that property is written: AddInfo and RemoveInfo refuse
// it, and so does every inventory write path a person or a sync plugin
// reaches.
func (b *Base) RecordDiscovery(d inventory.Discovery) {
	b.mu.Lock()
	defer b.mu.Unlock()
	old := b.props[inventory.DiscoveredProperty]
	value := d.Property()
	b.props[inventory.DiscoveredProperty] = value
	b.version++
	b.history = append(b.history, inventory.Revision{
		Version:   b.version,
		ChangedAt: time.Now().UTC(),
		Field:     inventory.DiscoveredProperty,
		OldValue:  old,
		NewValue:  value,
	})
}

func (b *Base) ShowInfo() inventory.Properties {
	return b.Properties()
}

func (b *Base) Version() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.version
}

func (b *Base) History() []inventory.Revision {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]inventory.Revision(nil), b.history...)
}

func (b *Base) State() inventory.LifecycleState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// The Revision.Field names a lifecycle transition and a tag change record
// under. Named once so every writer and every reader of the audit trail
// agree on the spelling; internal/inventory's retirement records under
// RevisionFieldState too.
const (
	RevisionFieldState = "state"
	RevisionFieldTags  = "tags"
)

// ChangeState moves the device to state and reports whether that changed
// anything. A change bumps the version and records a revision under
// RevisionFieldState holding the old and new state names, exactly as
// AddInfo does for a property, so a repository's optimistic-concurrency
// write sees a change to store and the audit trail says who moved a device
// out of quarantine and when. Setting the state a device already has
// records nothing.
func (b *Base) ChangeState(state inventory.LifecycleState) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if state == b.state {
		return false
	}
	old := b.state
	b.state = state
	b.version++
	b.history = append(b.history, inventory.Revision{
		Version:   b.version,
		ChangedAt: time.Now().UTC(),
		Field:     RevisionFieldState,
		OldValue:  old.String(),
		NewValue:  state.String(),
	})
	return true
}

// ChangeTags replaces the device's tags and reports whether that changed
// anything, recording a revision under RevisionFieldTags as ChangeState
// does. Tags are compared as a set, so the same tags in another order
// change nothing and record nothing.
func (b *Base) ChangeTags(tags []inventory.Tag) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sameTagSet(b.tags, tags) {
		return false
	}
	old := tagStrings(b.tags)
	b.tags = append([]inventory.Tag(nil), tags...)
	b.version++
	b.history = append(b.history, inventory.Revision{
		Version:   b.version,
		ChangedAt: time.Now().UTC(),
		Field:     RevisionFieldTags,
		OldValue:  old,
		NewValue:  tagStrings(tags),
	})
	return true
}

// sameTagSet reports whether a and b hold the same tags, in any order.
func sameTagSet(a, b []inventory.Tag) bool {
	count := make(map[inventory.Tag]int, len(a))
	for _, t := range a {
		count[t]++
	}
	for _, t := range b {
		count[t]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}

// tagStrings renders tags as the plain strings a revision stores.
func tagStrings(tags []inventory.Tag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = string(t)
	}
	return out
}

func (b *Base) Source() inventory.SourceAuthority { return b.source }
