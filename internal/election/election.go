// Package election provides a generic, reusable distributed leader
// election primitive built on lock.Manager, so any caller needing
// "exactly one of N replicas does X" constructs a LeaderElector against
// its own well-known key, instead of re-embedding the
// acquire/heartbeat/release state machine PATTERNS.md's "Leader Election
// (The Scheduler Pattern)" entry already names as one specific
// application of the general-purpose Distributed Lock primitive
// (PATTERNS.md's own "Distributed Lock" entry). PLAN.md Section 25 names
// this the "Leader elector" Build-Once Contract; this package is its one
// implementation.
package election

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
)

// electionInterval is how often a LeaderElector polls for the lease when
// it is not currently the leader, and how often it renews the lease when
// it is. electionTTL is the per-key TTL requested on every Acquire and
// KeepAlive call. Together they bound worst-case failover time after a
// non-graceful leader death (one that never calls Release) to roughly
// electionTTL + electionInterval: a kill landing immediately after a
// fresh renewal leaves the key alive server-side until electionTTL after
// that renewal, and a waiting replica notices at most electionInterval
// later, on its own next poll. electionTTL must be a whole number of
// seconds: internal/lock/nats.go's minPositiveTTL rejects sub-second
// TTL, since real per-key JetStream KV TTL only has whole-second
// granularity server-side.
const (
	electionInterval = 500 * time.Millisecond
	electionTTL      = 2 * time.Second
)

// releaseTimeout bounds how long a best-effort lease release is allowed
// to take, on both the graceful (ctx.Done()) and the renewal-failure
// paths, so Run always makes forward progress even against an
// unreachable or wedged lock store instead of hanging indefinitely on an
// unbounded context.Background() release call.
const releaseTimeout = 2 * time.Second

// LeaderElector runs a background lease-acquisition loop against a
// single, caller-supplied key, so that at any moment, at most one of any
// number of concurrently running LeaderElector instances constructed
// against the same lock.Manager and the same key observes IsLeader()
// true. Two LeaderElectors constructed with different keys never
// contend with each other, even against the same Manager.
type LeaderElector struct {
	manager    lock.Manager
	key        string
	onAcquired func()
	isLeader   atomic.Bool
}

// Option configures a LeaderElector constructed by NewLeaderElector.
type Option func(*LeaderElector)

// WithOnAcquired registers fn to run synchronously, on Run's own loop
// goroutine, exactly once each time this LeaderElector transitions from
// not-leader to leader (never on a tick where leadership is merely
// renewed). fn must return quickly: it runs inline on the same goroutine
// that must keep renewing the lease on schedule, so a slow or blocking
// fn directly delays the next renewal tick. fn must not call back into
// this LeaderElector. This is the seam a caller uses to react to
// acquiring leadership (for example, logging its own domain-specific
// message) without this generic package hardcoding any caller-specific
// wording.
func WithOnAcquired(fn func()) Option {
	return func(e *LeaderElector) {
		e.onAcquired = fn
	}
}

// NewLeaderElector constructs a LeaderElector that contends for key
// against mgr. key is opaque to this package: the caller owns its
// meaning and its collision domain, so a caller running two logically
// distinct elections must use two different keys.
func NewLeaderElector(mgr lock.Manager, key string, opts ...Option) *LeaderElector {
	e := &LeaderElector{
		manager: mgr,
		key:     key,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Run blocks until ctx is done, repeatedly attempting to acquire and
// renew this LeaderElector's own lease against key. On ctx.Done(), if
// this LeaderElector currently holds the lease, it is released
// best-effort (see releaseBestEffort) before Run returns, so a caller
// can perform an orderly handover instead of leaving the lease to expire
// on its own TTL (PATTERNS.md's "Graceful Shutdown" entry).
func (e *LeaderElector) Run(ctx context.Context) {
	ticker := time.NewTicker(electionInterval)
	defer ticker.Stop()

	var currentLease lock.Lease

	for {
		select {
		case <-ctx.Done():
			// Graceful handover: if we are shutting down and hold the
			// lease, release it immediately instead of leaving it to
			// expire on its own TTL, so a waiting replica can take over
			// on its very next poll rather than waiting out electionTTL.
			if currentLease != nil {
				e.releaseBestEffort(currentLease)
			}
			e.isLeader.Store(false)
			return

		case <-ticker.C:
			// Go's select does not prioritize among simultaneously ready
			// cases: if ctx is canceled at almost the same instant a tick
			// fires, this case can still be chosen over the ctx.Done()
			// case above. Deferring to the next loop iteration here
			// (rather than proceeding to call KeepAlive/Acquire with an
			// already-dead ctx) makes the graceful-shutdown path
			// deterministic: ticker.C cannot be ready again until a full
			// electionInterval elapses, so the next select only has
			// ctx.Done() to choose from.
			if ctx.Err() != nil {
				continue
			}

			if currentLease != nil {
				// We are the leader: prove it by renewing the lease.
				if err := currentLease.KeepAlive(ctx); err != nil {
					if ctx.Err() != nil {
						// ctx was canceled while this renewal call was
						// in flight: an ordinary race between shutdown
						// and a renewal that had already started, not a
						// genuine store-side failure. Handled exactly
						// like the graceful ctx.Done() case above
						// (release, step down, return), without that
						// case's own log-free treatment being
						// contradicted by a "renewal failed" warning
						// here for the identical situation.
						e.releaseBestEffort(currentLease)
						e.isLeader.Store(false)
						return
					}
					// Renewal failed: this lease can no longer be
					// trusted to still be held (it may have already
					// expired, or someone else may have reclaimed it).
					// Release it explicitly rather than merely dropping
					// the local reference, so any real, still-live hold
					// this process forgot about is not left dangling
					// until electionTTL expires it on its own.
					slog.Warn("leader election: lease renewal failed, releasing and stepping down",
						slog.String("key", e.key),
						slog.String("error", err.Error()),
					)
					e.releaseBestEffort(currentLease)
					currentLease = nil
					e.isLeader.Store(false)
				}
				continue
			}

			// We are not the leader: attempt to acquire the lease.
			lease, err := e.manager.Acquire(ctx, e.key, electionTTL, lock.AcquireOptions{})
			switch {
			case err == nil:
				currentLease = lease
				e.isLeader.Store(true)
				if e.onAcquired != nil {
					e.onAcquired()
				}
			case errors.Is(err, lock.ErrLockHeld):
				// Expected steady state for every non-leader replica on
				// every tick; deliberately not logged, since it would be
				// near-continuous noise at electionInterval cadence
				// across every waiting replica.
			case ctx.Err() != nil:
				// ctx was canceled while this acquire attempt was in
				// flight: an ordinary shutdown race, not a genuine
				// store-side failure worth logging as an error. The
				// loop's own ctx.Done() case handles the actual
				// shutdown on its next iteration.
			default:
				// A real error, not just losing the race or shutting
				// down: the lock store itself is unreachable, a
				// malformed key, or similar.
				slog.Error("leader election: lease acquisition failed",
					slog.String("key", e.key),
					slog.String("error", err.Error()),
				)
			}
		}
	}
}

// IsLeader reports whether this LeaderElector currently believes it
// holds the lease. Safe for concurrent use with Run.
func (e *LeaderElector) IsLeader() bool {
	return e.isLeader.Load()
}

// releaseBestEffort attempts to release lease within releaseTimeout,
// using a context independent of Run's own ctx: on the graceful
// ctx.Done() path, that ctx has already fired by construction and
// cannot be reused for the release call itself; on the renewal-failure
// path, ctx may or may not still be valid (a transient network blip is
// not necessarily ctx cancellation), and is deliberately not reused
// either, so this helper behaves identically from both call sites. An
// unbounded context.Background() release call could hang indefinitely
// against an unreachable or wedged lock store; bounding it here
// guarantees Run always returns (on the ctx.Done() path) or keeps making
// forward progress (on the renewal-failure path) regardless of the
// store's own health.
//
// A known, accepted race: if the immediately preceding KeepAlive was
// itself canceled while its own publish-and-await-ack round trip was in
// flight, the write can have durably succeeded server-side (advancing
// the key's revision) even though the client observed cancellation and
// never learned the new revision, leaving lease's own locally tracked
// revision stale. This call's CAS-based delete then fails with a
// wrong-last-sequence error, logged below, and the entry is left
// in place rather than freed early. This is not a correctness gap: the
// entry still expires on its own via electionTTL, so worst-case failover
// still lands within this package's own electionTTL+electionInterval
// bound (see the doc comment on those constants); this call is an
// optimization for a faster-than-TTL handover, and losing this specific
// race just forgoes the optimization for that one cycle, not the bound
// itself.
func (e *LeaderElector) releaseBestEffort(lease lock.Lease) {
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	if err := lease.Release(ctx); err != nil {
		slog.Warn("leader election: best-effort lease release failed",
			slog.String("key", e.key),
			slog.String("error", err.Error()),
		)
	}
}
