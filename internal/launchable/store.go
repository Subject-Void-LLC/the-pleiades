// This file is the read port over the launchables table.
//
// It reads only the columns every type shares, which is what makes it useful:
// a picker, a validator and a schedule's store all need the same four facts
// (which row, what sort of thing, what it is called, whose it is) and none of
// them should have to ask a type-specific store for them. Anything beyond
// those facts belongs to the target's own store.
//
// There is no write port. A launchable row is written by whichever store owns
// the target it stands for, inside the same transaction, because the row is
// that target's identity as something launchable rather than a record of its
// own. A second writer here would be a second place the two could disagree.
package launchable

import "context"

// Query narrows a listing.
type Query struct {
	// OrganizationID limits the listing to one tenant. Zero means every
	// tenant, which is what an administrative listing is; a caller narrowing
	// to a person's reach passes their organization.
	OrganizationID int

	// Types limits the listing to these launchable types. Empty means every
	// registered type, which is what a picker offering all of them asks for.
	Types []string

	// Limit caps the rows returned. Zero means the store's own default, and
	// a value above its ceiling is lowered to it, so no caller can ask a
	// page to be unbounded.
	Limit int
}

// Store reads launchables.
type Store interface {
	// Get returns one launchable by its own id, or ErrNotFound.
	Get(ctx context.Context, id int) (Target, error)

	// List returns launchables matching q, ordered by type and then name, so
	// a picker's groups arrive already grouped and a listing does not
	// reorder itself between reads.
	List(ctx context.Context, q Query) ([]Target, error)
}
