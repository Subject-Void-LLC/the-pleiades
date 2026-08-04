package lock

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// inProcessManager is an in-memory implementation of Manager, suitable for
// single-process deployments, tests, and local development where a
// distributed lock store such as NATS JetStream is unavailable or
// unnecessary. It honors the per-call ttl argument: a holder whose deadline
// has passed is pruned and its slot becomes reclaimable by the next Acquire
// call for the same itemID, and it supports every AcquireOptions
// combination (Mode, ContentionPolicy, Priority) natsLockManager does. It
// only coordinates within its own process; it does not replace a
// distributed Manager for multi-runner deployments.
type inProcessManager struct {
	mu        *sync.Mutex                    // guards locks and nextToken together
	locks     map[string]*inProcessLockEntry // itemID to the entry currently holding it
	nextToken uint64                         // monotonically increasing, never reused, identifies a holder
}

// inProcessLockEntry tracks every current holder of a single itemID.
// Exclusive mode entries only ever have one holder; shared mode entries can
// have any number.
type inProcessLockEntry struct {
	mode    Mode
	holders map[uint64]time.Time // token to that holder's own locally-computed deadline
}

// NewInProcessManager builds a Manager backed entirely by in-process
// memory. It requires no external services and is substitutable anywhere a
// lock.Manager is expected. It only guards against concurrent access within
// this process; it does not coordinate locks across multiple runner
// instances the way natsLockManager does.
func NewInProcessManager() Manager {
	return &inProcessManager{
		mu:    &sync.Mutex{},
		locks: make(map[string]*inProcessLockEntry),
	}
}

// Acquire attempts to lock itemID according to opts. See Manager.Acquire.
func (m *inProcessManager) Acquire(ctx context.Context, itemID string, ttl time.Duration, opts AcquireOptions) (Lease, error) {
	return acquireWithContention(ctx, opts, func(ctx context.Context) (Lease, error) {
		return m.tryAcquireOnce(ctx, itemID, ttl, opts)
	})
}

// tryAcquireOnce is a single, non-retrying acquire attempt: PolicyReject's
// entire behavior, and the primitive acquireWithContention retries under
// PolicyQueue/PolicyPriority.
func (m *inProcessManager) tryAcquireOnce(ctx context.Context, itemID string, ttl time.Duration, opts AcquireOptions) (Lease, error) {
	// There is no blocking I/O below, so this is the only point where a
	// canceled or expired context can actually change the outcome.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateTTL(ttl); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, held := m.locks[itemID]
	if held {
		pruneExpiredHolders(entry)
		if len(entry.holders) == 0 {
			held = false
			delete(m.locks, itemID)
		}
	}

	if held {
		// Only a shared request joining an existing shared entry can ever
		// succeed against a still-live holder; every other combination
		// (exclusive-vs-anything, or shared-vs-live-exclusive) contends.
		// Priority never overrides this: it only changes how fast a
		// caller retries in acquireWithContention, never what a single
		// attempt is allowed to do to a live holder (see queue.go's own
		// doc comment for why).
		if opts.Mode != ModeShared || entry.mode != ModeShared {
			return nil, ErrLockHeld
		}
		m.nextToken++
		token := m.nextToken
		entry.holders[token] = time.Now().Add(ttl)
		return &inProcessLease{manager: m, itemID: itemID, token: token, ttl: ttl, deadline: time.Now().Add(ttl)}, nil
	}

	m.nextToken++
	token := m.nextToken
	m.locks[itemID] = &inProcessLockEntry{
		mode:    opts.Mode,
		holders: map[uint64]time.Time{token: time.Now().Add(ttl)},
	}
	return &inProcessLease{manager: m, itemID: itemID, token: token, ttl: ttl, deadline: time.Now().Add(ttl)}, nil
}

// Close is a no-op for inProcessManager. There is no underlying connection
// or external resource to release; all state lives in this process's own
// memory and is reclaimed by the garbage collector like any other value.
func (m *inProcessManager) Close() error {
	return nil
}

// pruneExpiredHolders removes every holder of entry whose deadline has
// passed, so a caller evaluating contention against entry only ever sees
// holders that are still genuinely alive. Callers must hold the owning
// manager's mu before calling this.
func pruneExpiredHolders(entry *inProcessLockEntry) {
	now := time.Now()
	for token, deadline := range entry.holders {
		if now.After(deadline) {
			delete(entry.holders, token)
		}
	}
}

// inProcessLease represents a single successful Acquire against
// inProcessManager. Its token must still match one of the owning entry's
// current holders for KeepAlive or Release to succeed; a mismatch means the
// hold expired and was pruned, or was released, by the time the call ran.
type inProcessLease struct {
	manager  *inProcessManager
	itemID   string
	token    uint64
	ttl      time.Duration // reapplied by KeepAlive
	deadline time.Time     // this lease's own locally-computed deadline, from this process's own monotonic-carrying time.Now(); never read back from itemID's stored entry, and never compared against a value produced by another process
}

// ID returns the itemID this lease was acquired for. More than one lease
// can share an itemID under ModeShared, so ID alone does not uniquely
// identify a lease; it matches natsLease.ID's existing behavior.
func (l *inProcessLease) ID() string {
	return l.itemID
}

// KeepAlive extends this lease's deadline by its original ttl, proving to
// the manager that the holder is still alive. It first checks this lease's
// own locally-computed deadline (see inProcessLease.deadline) and fails
// fast, with no map access, if that has already passed: Section 16's
// monotonic-time clock-drift mitigation made concrete, since a process that
// has itself observed it is running past its own deadline self-fences
// immediately rather than only ever discovering staleness by losing a
// comparison against the shared entry. It also returns an error, and makes
// no change, if this lease has since been superseded (expired and pruned,
// or released) by the time the call runs.
func (l *inProcessLease) KeepAlive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Now().After(l.deadline) {
		return fmt.Errorf("lease %s is no longer current: local deadline already passed", l.itemID)
	}

	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()

	entry, held := l.manager.locks[l.itemID]
	if !held {
		return fmt.Errorf("lease %s is no longer current", l.itemID)
	}
	if _, holding := entry.holders[l.token]; !holding {
		return fmt.Errorf("lease %s is no longer current", l.itemID)
	}

	l.deadline = time.Now().Add(l.ttl)
	entry.holders[l.token] = l.deadline
	return nil
}

// Release frees this lease's hold on itemID, provided no one else has
// since superseded it. It returns an error, and leaves the map untouched,
// if this lease is stale, mirroring how the NATS adapter's revision-based
// Delete fails once superseded, so a straggling Release from an expired
// lease can never delete a different holder's entry. Releasing the last
// remaining holder of an itemID removes the entry entirely.
func (l *inProcessLease) Release(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	l.manager.mu.Lock()
	defer l.manager.mu.Unlock()

	entry, held := l.manager.locks[l.itemID]
	if !held {
		return fmt.Errorf("lease %s is no longer current", l.itemID)
	}
	if _, holding := entry.holders[l.token]; !holding {
		return fmt.Errorf("lease %s is no longer current", l.itemID)
	}

	delete(entry.holders, l.token)
	if len(entry.holders) == 0 {
		delete(l.manager.locks, l.itemID)
	}
	return nil
}
