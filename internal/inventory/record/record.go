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

	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
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

// Declares reports whether this device's classification assigned it name.
// A concrete type's HasCapability ANDs this with capability.Implements, so
// a device can only advertise a capability it both claims and structurally
// satisfies (the CODE_SCAFFOLD binding rule).
func (b *Base) Declares(name capability.Name) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.caps[name]
	return ok
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

func (b *Base) State() inventory.LifecycleState { return b.state }

func (b *Base) Source() inventory.SourceAuthority { return b.source }
