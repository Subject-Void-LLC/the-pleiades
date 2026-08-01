package inventory

import "context"

// Iterator provides memory-safe streaming of inventory items.
// It acts as a cursor over a database result set (e.g., pgx.Rows),
// preventing Out-Of-Memory (OOM) crashes when querying massive groups.
type Iterator interface {
	// Next advances the cursor. It returns true if an item is available,
	// or false if the end is reached or an error occurs.
	Next(ctx context.Context) bool

	// Item returns the current InventoryItem.
	Item() InventoryItem

	// Error returns any error encountered during iteration.
	Error() error

	// Close releases the underlying database cursor.
	Close() error
}

// Repository defines the data access methods for the inventory state.
type Repository interface {
	// GetGroup returns an Iterator to safely stream all devices within a group.
	GetGroup(ctx context.Context, groupName string) (Iterator, error)
}
