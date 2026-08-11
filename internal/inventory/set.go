package inventory

import (
	"context"
	"errors"
	"time"
)

// Set is what the API and the UI call an Inventory: a named, shareable
// collection of devices that a runbook can be dispatched against.
//
// The Go type is called Set rather than Inventory because this package
// already owns the word. inventory.Repository is the device repository and
// pkg/inventory.InventoryItem is a single managed device, both of which
// predate this type by many phases; a second, differently-shaped Inventory
// beside them would make "the inventory" ambiguous in every sentence and
// every review comment. The user-facing vocabulary is unaffected -- the ent
// entity is Inventory, the API path is /inventories, the UI heading says
// Inventories -- which is where CLAUDE.md's naming rule actually applies.
//
// A Set holds groups, devices attached directly with no intervening group,
// or both. Groups are the existing nested, DAG-shaped device grouping
// (internal/ent/schema/group.go); direct devices are the "ungrouped hosts"
// case every real inventory eventually accumulates.
type Set struct {
	// ID is the stable numeric identifier. It is an int rather than an
	// opaque string because it is what a RoleBinding's ScopeID points at,
	// and that column is an int for every other scope already.
	ID int

	// Name is unique within an organization, not globally. Two tenants
	// both having a "production" inventory is the ordinary case.
	Name string

	Description string

	// OrganizationID is the tenancy boundary. A Set always has one: an
	// inventory belonging to no organization could be resolved against no
	// organization scope, which would make it reachable either by everyone
	// or by nobody depending on which way the resolver happened to fail.
	OrganizationID int

	// Owner is the subject that created it. It records authorship, never
	// authority: a grant lives on a Team's RoleBinding, so owning a Set
	// confers no permission over it (PLAN.md Section 18.2 -- roles are
	// assigned to Teams, never directly to Users). It is here so the
	// question "who lent our hosts to that team" has an answer.
	Owner string

	// GroupIDs and DeviceIDs are what this Set contains.
	GroupIDs  []int
	DeviceIDs []int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// DeviceCount is what a list view renders without loading the members.
// Direct devices plus grouped ones is deliberately not computed here: a
// group's membership is a query, and a list of twenty inventories would
// mean twenty more of them. SetStore.List fills this in with one aggregate
// where the backing store can.
func (s Set) DeviceCount() int { return len(s.DeviceIDs) }

// ErrSetNotFound is returned when an id or name names no Set. Callers use
// errors.Is rather than comparing directly, matching every other port here.
var ErrSetNotFound = errors.New("inventory set not found")

// ErrSetExists is returned when a name is already taken within its
// organization.
var ErrSetExists = errors.New("inventory set already exists in this organization")

// SetQuery is a list request against SetStore.
type SetQuery struct {
	// OrganizationIDs restricts the result to these tenants. Empty means
	// no restriction, which only a system-scoped caller should ever be
	// given -- the store does not decide that, the caller does, because
	// authorization is the admission chain's job and a port that filtered
	// on its own would be a second place deciding it.
	OrganizationIDs []int

	// After is a keyset cursor: the last id of the previous page. Offset
	// paging over a table being written to skips and repeats rows, which
	// on a list somebody is reading means an inventory silently missing.
	After int

	// Limit bounds the page. Zero means the store's own default.
	Limit int

	// Search filters on name, case-insensitively. Empty means no filter.
	Search string
}

// SetStore persists Sets.
//
// It is a port with an ent adapter behind it, so nothing above this line
// imports generated code -- the same split RoleBindingRepository already
// makes for the same reason.
type SetStore interface {
	// Create persists a new Set and returns it with its assigned ID.
	// It returns ErrSetExists if the name is taken in that organization.
	Create(ctx context.Context, set Set) (Set, error)

	// Get returns one Set by id, or ErrSetNotFound.
	Get(ctx context.Context, id int) (Set, error)

	// List returns a page of Sets matching q, newest id last.
	List(ctx context.Context, q SetQuery) ([]Set, error)

	// Update saves a Set's name, description and membership. The
	// organization is not updatable: moving an inventory between tenants
	// would silently re-scope every RoleBinding pointing at it, which is a
	// migration rather than an edit.
	Update(ctx context.Context, set Set) error

	// Delete removes a Set. The devices and groups it referenced are
	// untouched -- an inventory is a view onto them, not their owner, and
	// deleting a shared collection must never delete the fleet.
	Delete(ctx context.Context, id int) error

	// SetsForDevice returns the ids of every Set a device is reachable
	// through, directly or through any group containing it.
	//
	// This is what fills ScopeTarget.InventoryIDs before an access check.
	// It is a store method rather than something the caller assembles,
	// because getting it wrong in the narrowing direction silently denies
	// a legitimate share, and getting it wrong in the widening direction
	// silently grants one.
	SetsForDevice(ctx context.Context, deviceID int) ([]int, error)
}
