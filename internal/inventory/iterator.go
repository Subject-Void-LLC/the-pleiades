package inventory

import (
	"context"
	"errors"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// Iterator provides memory-safe streaming of inventory items. It acts as a
// cursor over a backing result set (a database query, a YAML file already
// read into memory), preventing out-of-memory crashes when querying
// massive groups.
type Iterator interface {
	// Next advances the cursor. It returns true if an item is available,
	// or false if the end is reached or an error occurs.
	Next(ctx context.Context) bool

	// Item returns the current InventoryItem.
	Item() inventory.InventoryItem

	// Error returns any error encountered during iteration.
	Error() error

	// Close releases the underlying cursor or file handle.
	Close() error
}

// ErrVersionConflict is returned by Save when the stored row moved on
// after the item was loaded, meaning another writer changed it in between.
// The caller must reload and reapply rather than retrying blindly: the
// change it computed was based on state that no longer exists.
var ErrVersionConflict = errors.New("inventory item was modified by another writer")

// Repository defines the data access methods for the inventory state. It
// is the pluggable port every tier's inventory backend satisfies: an
// ent-backed repository at Crawl and above, a YAML-backed one at Walk.
type Repository interface {
	// GetGroup returns an Iterator to safely stream all devices within a
	// group. Items it yields carry their stored version but not their
	// audit trail: loading history for every row of a list view would be
	// a query per device for data a list view does not display.
	GetGroup(ctx context.Context, groupName string) (Iterator, error)

	// GetByName returns a single item by its unique name, including its
	// full audit trail. This is the read counterpart to Save: it is how a
	// caller reloads after an ErrVersionConflict, and how anything that
	// needs History rather than just current state fetches an item.
	GetByName(ctx context.Context, name string) (inventory.InventoryItem, error)

	// Save persists an item's mutated properties, its lifecycle state, and
	// every Revision recorded since it was loaded.
	//
	// The write is conditional on the item's stored version still matching
	// the version it was hydrated at. If another writer got there first,
	// Save changes nothing and returns ErrVersionConflict. This is what
	// makes Version an optimistic-concurrency token rather than a counter,
	// and it is the reason two runners acting on one device cannot silently
	// lose one another's changes.
	//
	// Save is a no-op returning nil when the item has no unsaved changes,
	// so a caller may call it unconditionally after a mutation pass.
	Save(ctx context.Context, item inventory.InventoryItem) error
}

// versioned is satisfied by any item that can report the version it was
// hydrated at, which Save needs to build its conditional predicate. It is
// kept out of inventory.InventoryItem on purpose: the public SDK contract
// describes what a device IS, and the version a row was read at is a
// persistence concern that no third-party device type should have to
// implement (Interface Segregation, PATTERNS.md).
type versioned interface {
	BaseVersion() uint64
}
