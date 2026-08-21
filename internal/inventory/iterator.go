package inventory

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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

// ErrItemNotFound is returned by GetByName when no item carries that name.
// Every Repository adapter wraps it rather than returning a bare formatted
// error, because callers must be able to tell "this device is new" apart
// from "the backend failed". A sync plugin's reconciliation pass is the
// first real consumer: an unwrapped not-found would make it treat a broken
// database as a fleet of brand new devices and write duplicates.
var ErrItemNotFound = errors.New("inventory item not found")

// ErrItemExists is returned by Create when an item with that name or ID is
// already stored. Create never overwrites: a sync plugin that rediscovers a
// device it already onboarded must reconcile it through Save, so the
// existing version token and audit trail survive rather than being reset by
// a second insert.
var ErrItemExists = errors.New("inventory item already exists")

// ErrSelectorUnsupported is returned by GetGroup when a Selector asks for a
// restriction this backend cannot apply.
//
// Returned rather than ignored, and only for restrictions whose omission
// would widen the result. A backend that cannot narrow and says nothing has
// answered a different question than the one asked, and on a dispatch the
// difference between "the devices in this inventory" and "every device" is
// the entire safety boundary.
var ErrSelectorUnsupported = errors.New("inventory backend cannot apply this selector")

// Repository defines the data access methods for the inventory state. It
// is the pluggable port every tier's inventory backend satisfies: an
// ent-backed repository at Crawl and above, a YAML-backed one at Walk.
type Repository interface {
	// GetGroup returns an Iterator to safely stream every device matching
	// sel. Items it yields carry their stored version but not their
	// audit trail: loading history for every row of a list view would be
	// a query per device for data a list view does not display.
	GetGroup(ctx context.Context, sel inventory.Selector) (Iterator, error)

	// GetByName returns a single item by its unique name, including its
	// full audit trail. This is the read counterpart to Save: it is how a
	// caller reloads after an ErrVersionConflict, and how anything that
	// needs History rather than just current state fetches an item.
	GetByName(ctx context.Context, name string) (inventory.InventoryItem, error)

	// Create inserts a brand new item, returning ErrItemExists if one is
	// already stored under that name or ID.
	//
	// It is separate from Save because the two have genuinely different
	// preconditions and failure modes. Save is a conditional update guarded
	// by a version token and is a no-op when nothing changed; Create has no
	// prior version to condition on and must fail loudly rather than
	// silently doing nothing. Folding them into one upsert would mean the
	// caller could no longer tell a first-time onboard from a re-sync,
	// which is exactly the distinction a sync plugin's reconciliation
	// report is made of.
	//
	// Until this method existed the Repository port had no write path at
	// all for new devices: add-host wrote hosts.yaml directly, bypassing
	// the port, and no sync plugin could onboard anything.
	Create(ctx context.Context, item inventory.InventoryItem) error

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

	// Retire transitions the item stored under name to
	// inventory.StateArchived, recording the transition as a Revision, and
	// returns ErrItemNotFound when no such item exists.
	//
	// This is what the API's DELETE verb performs, and it is deliberately
	// a lifecycle transition rather than a row removal. Every Revision is
	// Immutable() by schema (internal/ent/schema/revision.go) and the
	// revisions edge carries no cascade, so deleting a device would mean
	// either destroying the audit trail the schema exists to protect or
	// violating its foreign key. PLAN.md's lifecycle already models
	// retirement directly (StateDecommissioning, StateArchived), and
	// LifecycleState.CanExecute() already refuses every state but Active,
	// so an archived device stops being a valid runbook target the moment
	// this returns.
	//
	// It is idempotent, because the HTTP verb it backs is: retiring an
	// already-archived item changes nothing, records no second Revision,
	// and returns nil rather than an error. A caller that needs to know
	// whether it was the one to retire the item should read State first.
	//
	// It is deliberately on this port rather than reached around it.
	// NewReadOnlyRepository's whole argument is that a wrapper cannot be
	// forgotten in one branch of one adapter; a delete path outside the
	// port would silently void the simulate-first guarantee for the one
	// operation that is hardest to undo.
	Retire(ctx context.Context, name string) error

	// GroupAncestry returns every group and inventory deviceName is
	// reachable through, transitively, ordered least specific (nearer a
	// root of the DAG) to most specific (deviceName's own direct group
	// membership) last. It exists so a caller can fold the chain through
	// pkg/policy.Resolve to answer "what does the hierarchy say for this
	// device," Phase 72's first consumer being a device's configured
	// bastion/hop-chain route (AGENTS.md's hierarchical policy
	// principle: the most specific level wins).
	//
	// Group nesting and group/inventory membership are both DAG-shaped,
	// not tree-shaped (a group can have more than one parent, and can
	// belong to more than one inventory), so there is no single natural
	// linear order. See entRepository's own implementation for the exact
	// ordering and tiebreak rule it uses; a caller needs only the
	// contract that the result is deterministic and least-to-most
	// specific.
	//
	// A device reachable through no group or inventory (or a Repository
	// with no such hierarchy at all, as the Walk-tier file-backed
	// implementation is) returns a nil slice and a nil error: "nothing
	// configured at any level" is a normal outcome, not a failure,
	// matching pkg/policy.Resolve's own empty-layers contract.
	GroupAncestry(ctx context.Context, deviceName string) ([]HierarchyLayer, error)
}

// HierarchyLayer is one named level in a device's group/inventory
// ancestry (see Repository.GroupAncestry), carrying that level's own
// Properties bag (internal/ent/schema's Group.properties or
// Inventory.properties) for a caller to fold through pkg/policy.Resolve.
type HierarchyLayer struct {
	// Name identifies the group or inventory this layer came from, for
	// pkg/policy.Result.Layers reporting and for error messages.
	Name string

	// Properties is that group's or inventory's own settings bag,
	// exactly as stored (possibly nil, when the level exists but has no
	// properties configured).
	Properties map[string]interface{}
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
