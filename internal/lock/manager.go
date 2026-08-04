// Package lock provides distributed concurrency control to prevent split-brain.
package lock

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrLockHeld is returned when a device is already locked by another process
// in a way the caller's AcquireOptions cannot resolve (see Manager.Acquire).
var ErrLockHeld = errors.New("lock already held")

// validateTTL rejects a negative ttl outright, with a clear domain error,
// shared by both adapters. natsLockManager layers one additional,
// NATS-specific check on top (see nats.go's own MinPositiveTTL): this
// function alone is the complete contract inProcessManager enforces, since
// a pure in-memory map has no external granularity constraint to honor.
func validateTTL(ttl time.Duration) error {
	if ttl < 0 {
		return fmt.Errorf("ttl must be non-negative, got %v", ttl)
	}
	return nil
}

// Mode selects whether an Acquire call wants exclusive or shared access to
// itemID, mirroring PLAN.md Section 13's lock granularity table. The zero
// value is ModeExclusive, so every pre-existing caller that never sets Mode
// keeps today's only behavior.
type Mode int

const (
	// ModeExclusive admits exactly one holder; every other Acquire call
	// against the same itemID, of either Mode, contends. Used for a
	// runbook executing changes against a device.
	ModeExclusive Mode = iota

	// ModeShared admits any number of concurrent holders, provided none of
	// them (or any contending caller) requests ModeExclusive. Used for
	// simulate, show-info, and fact-query style reads that must not block
	// each other but must still exclude a concurrent exclusive writer.
	ModeShared
)

// String returns Mode's name, used in error messages and logs.
func (m Mode) String() string {
	switch m {
	case ModeExclusive:
		return "exclusive"
	case ModeShared:
		return "shared"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// ContentionPolicy selects how Acquire behaves when itemID is already held
// in a way that conflicts with the requested Mode, mirroring PLAN.md
// Section 13's three contention policies. The zero value is PolicyReject,
// so every pre-existing caller that never sets Policy keeps today's only
// (implicit) behavior: fail immediately with ErrLockHeld.
type ContentionPolicy int

const (
	// PolicyReject fails immediately with ErrLockHeld on contention. This
	// is every adapter's behavior today, now named explicitly.
	PolicyReject ContentionPolicy = iota

	// PolicyQueue waits, retrying with jittered exponential backoff
	// (pkg/retry.Backoff), until itemID becomes available or ctx is done.
	// This is "wait with backoff," not a FIFO ticket queue: strict
	// acquisition ordering would need a separate queue/ticket primitive,
	// which this phase does not build.
	PolicyQueue

	// PolicyPriority allows a caller to eagerly reclaim an itemID whose
	// current holder's own declared deadline has already elapsed, instead
	// of waiting for the distributed store's own lazy expiry sweep to
	// catch up. It never revokes a lock that is still within its declared
	// window: doing so would let two callers physically execute against
	// the same device concurrently, since neither engine.Executor's
	// per-device locks nor most other callers call KeepAlive to prove
	// they are still alive mid-hold. See AcquireOptions.Priority.
	PolicyPriority
)

// String returns ContentionPolicy's name, used in error messages and logs.
func (p ContentionPolicy) String() string {
	switch p {
	case PolicyReject:
		return "reject"
	case PolicyQueue:
		return "queue"
	case PolicyPriority:
		return "priority"
	default:
		return fmt.Sprintf("ContentionPolicy(%d)", int(p))
	}
}

// AcquireOptions configures a single Acquire call. Its zero value
// (AcquireOptions{}) is exactly today's only behavior (exclusive, reject on
// contention, priority irrelevant), so every existing caller can adopt this
// type with a purely mechanical, behavior-preserving update.
type AcquireOptions struct {
	// Mode selects exclusive or shared access. Defaults to ModeExclusive.
	Mode Mode

	// Policy selects how contention is handled. Defaults to PolicyReject.
	Policy ContentionPolicy

	// Priority is consulted only when Policy is PolicyPriority. Among
	// callers racing to reclaim the same already-expired itemID, a higher
	// Priority wins the eager steal; it has no effect against a lock that
	// is still within its declared window (see PolicyPriority).
	Priority int
}

// Manager prevents simultaneous execution against a single InventoryItem.
type Manager interface {
	// Acquire attempts to lock a specific InventoryItem according to opts.
	// It returns ErrLockHeld if itemID is held in a way opts cannot
	// resolve (see Mode, ContentionPolicy), or ctx's own error if ctx is
	// canceled or its deadline is exceeded, including while PolicyQueue is
	// still waiting.
	Acquire(ctx context.Context, itemID string, ttl time.Duration, opts AcquireOptions) (Lease, error)

	// Close releases any underlying connections to the distributed lock store.
	Close() error
}

// Lease represents an active hold (exclusive or shared) on a device.
type Lease interface {
	// ID returns the unique identifier of the lock lease.
	ID() string

	// KeepAlive extends the lock duration. Runners must call this periodically
	// to prove they are still alive.
	KeepAlive(ctx context.Context) error

	// Release frees the lock so other tasks can execute against the device.
	Release(ctx context.Context) error
}

// AcquireAll attempts to acquire every id in itemIDs, in order, against mgr.
// If any Acquire call fails, every lease already acquired earlier in this
// call is released (best-effort; a release failure is not itself returned,
// since the original acquire error is the one the caller needs to act on)
// before the original error is returned, so a caller never ends up holding
// only part of the set. This is PLAN.md Section 13's "all-at-plan-time"
// acquisition strategy: acquire everything up front, all-or-nothing, before
// any execution begins. "Per-device-as-reached" (the other named strategy)
// needs no equivalent helper: it is simply calling mgr.Acquire directly,
// once per device, as execution reaches it, which is already what
// engine.Executor.runOne does.
func AcquireAll(ctx context.Context, mgr Manager, itemIDs []string, ttl time.Duration, opts AcquireOptions) ([]Lease, error) {
	leases := make([]Lease, 0, len(itemIDs))
	for _, id := range itemIDs {
		lease, err := mgr.Acquire(ctx, id, ttl, opts)
		if err != nil {
			for _, held := range leases {
				_ = held.Release(context.Background())
			}
			return nil, fmt.Errorf("failed to acquire %q while acquiring all-at-plan-time: %w", id, err)
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

// CapacityCounter is the Bulkhead primitive (PATTERNS.md's "Bulkhead
// (Semaphore Isolation)" entry) for admission control: a bounded resource
// pool a caller must reserve capacity from before starting expensive work,
// and return when done. Declared here per PLAN.md Section 25's Build-Once
// table ("Distributed capacity counter... Declare Phase 3, implement Phase
// 24"): Phase 24 (Dependency Manager & Capacity Admission) is this port's
// first implementation and caller, using fork-based memory/CPU accounting.
// No adapter ships with this phase; declaring the shape now is what stops
// Phase 24 from shipping a bespoke semaphore that a later phase has to
// replace, Section 25's own stated cost of deferring past this deadline.
type CapacityCounter interface {
	// TryAcquire attempts to reserve n units of capacity, returning false
	// (never blocking) if fewer than n units are currently free.
	TryAcquire(ctx context.Context, n int) (bool, error)

	// Release returns n previously reserved units to the pool.
	Release(ctx context.Context, n int) error
}
