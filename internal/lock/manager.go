// Package lock provides distributed concurrency control to prevent split-brain.
package lock

import (
	"context"
	"errors"
	"time"
)

// ErrLockHeld is returned when a device is already locked by another process.
var ErrLockHeld = errors.New("lock already held")

// Manager prevents simultaneous execution against a single InventoryItem.
type Manager interface {
	// Acquire attempts to lock a specific InventoryItem.
	// It returns an error if the device is already locked by another runner.
	Acquire(ctx context.Context, itemID string, ttl time.Duration) (Lease, error)
}

// Lease represents an active, exclusive hold on a device.
type Lease interface {
	// ID returns the unique identifier of the lock lease.
	ID() string

	// KeepAlive extends the lock duration. Runners must call this periodically
	// to prove they are still alive.
	KeepAlive(ctx context.Context) error

	// Release frees the lock so other tasks can execute against the device.
	Release(ctx context.Context) error
}
