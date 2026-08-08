package lock_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// runManagerConformance exercises the subset of lock.Manager's contract
// that every adapter can honestly guarantee today, regardless of backend.
// Both adapters now honor the per-call ttl argument and support every
// AcquireOptions combination identically (natsLockManager's per-key TTL
// requires nats-server 2.11+; see nats.go's own doc comment), so this suite
// no longer needs to exclude ttl-expiry timing assertions the way it once
// did before natsLockManager honored ttl at all. One real, stated adapter
// asymmetry remains and is deliberately not exercised here:
// natsLockManager additionally rejects a positive ttl below one second
// (nats.go's own minPositiveTTL, a real JetStream server-side granularity
// constraint), while inProcessManager accepts any non-negative ttl, since a
// pure in-memory map has no such external constraint to honor.
//
// newManager must return a Manager ready to accept Acquire calls. It may
// return a brand new instance each call (as the in-process test does) or
// the same already-connected instance every time (as the NATS test does);
// each subtest below uses itemIDs unique to that subtest so either style
// is safe.
func runManagerConformance(t *testing.T, newManager func() lock.Manager) {
	t.Helper()

	t.Run("ThunderingHerd", func(t *testing.T) {
		conformanceThunderingHerd(t, newManager())
	})

	t.Run("ReleaseThenReacquire", func(t *testing.T) {
		conformanceReleaseThenReacquire(t, newManager())
	})

	t.Run("IndependentItemIDs", func(t *testing.T) {
		conformanceIndependentItemIDs(t, newManager())
	})

	t.Run("KeepAliveOnValidLease", func(t *testing.T) {
		conformanceKeepAliveOnValidLease(t, newManager())
	})

	t.Run("SharedHoldersConcurrent", func(t *testing.T) {
		conformanceSharedHoldersConcurrent(t, newManager())
	})

	t.Run("SharedBlocksExclusiveAndViceVersa", func(t *testing.T) {
		conformanceSharedBlocksExclusiveAndViceVersa(t, newManager())
	})

	t.Run("QueuePolicyWaitsThenSucceeds", func(t *testing.T) {
		conformanceQueuePolicyWaitsThenSucceeds(t, newManager())
	})

	t.Run("SharedHoldersChurn", func(t *testing.T) {
		conformanceSharedHoldersChurn(t, newManager())
	})

	t.Run("PriorityPolicyWaitsThenSucceeds", func(t *testing.T) {
		conformancePriorityPolicyWaitsThenSucceeds(t, newManager())
	})

	t.Run("ContentionPolicyRespectsContextDeadline", func(t *testing.T) {
		conformanceContentionPolicyRespectsContextDeadline(t, newManager())
	})

	t.Run("AcquireAllReleasesOnPartialFailure", func(t *testing.T) {
		conformanceAcquireAllReleasesOnPartialFailure(t, newManager())
	})
}

// conformanceThunderingHerd races numGoroutines callers to Acquire the same
// itemID against mgr simultaneously and asserts exactly one wins while the
// rest see ErrLockHeld and nothing else. This is the backend-agnostic core
// of TestThunderingHerdLocking, generalized so both adapters can run it, and
// matches this phase's own Release Gate text ("100 goroutines... exactly 1
// succeeds") literally, not just approximately.
func conformanceThunderingHerd(t *testing.T, mgr lock.Manager) {
	t.Helper()

	const numGoroutines = 100
	const itemID = "conformance-herd-target"

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// startingGate holds every goroutine at the gate so Acquire calls fire
	// as close to simultaneously as possible, maximizing race pressure.
	var startingGate sync.WaitGroup
	startingGate.Add(1)

	var successCount int32
	var lockedCount int32
	var unexpectedErrors int32
	var winningLease lock.Lease // written by at most one goroutine; see note below

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			startingGate.Wait() // block until the starting gun fires

			lease, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
			switch {
			case err == nil:
				// Exactly one goroutine can reach this branch, because
				// only one Acquire call can win the underlying CAS; the
				// write to winningLease is therefore never contended.
				atomic.AddInt32(&successCount, 1)
				winningLease = lease
			case errors.Is(err, lock.ErrLockHeld):
				atomic.AddInt32(&lockedCount, 1)
			default:
				atomic.AddInt32(&unexpectedErrors, 1)
				t.Errorf("unexpected error during acquire: %v", err)
			}
		}()
	}

	startingGate.Done() // release the hounds
	wg.Wait()           // sync.WaitGroup.Wait happens-after every Done, making winningLease's write visible below

	if unexpectedErrors > 0 {
		t.Fatalf("encountered %d unexpected errors during thundering herd", unexpectedErrors)
	}
	if successCount != 1 {
		t.Fatalf("expected exactly 1 goroutine to acquire the lock, got %d. split brain detected", successCount)
	}
	if lockedCount != numGoroutines-1 {
		t.Fatalf("expected %d goroutines to hit ErrLockHeld, got %d", numGoroutines-1, lockedCount)
	}

	// Clean up so itemID does not leak into a later subtest that reuses
	// the same manager instance (the NATS conformance test shares one
	// manager, and thus one underlying bucket, across all subtests).
	if err := winningLease.Release(context.Background()); err != nil {
		t.Errorf("failed to release winning lease: %v", err)
	}
}

// conformanceReleaseThenReacquire asserts that releasing a lease
// immediately frees its itemID for a fresh Acquire, independent of
// whatever ttl was requested.
func conformanceReleaseThenReacquire(t *testing.T, mgr lock.Manager) {
	t.Helper()
	const itemID = "conformance-release-reacquire"

	lease, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	lease2, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("second acquire after release failed: %v", err)
	}
	if err := lease2.Release(context.Background()); err != nil {
		t.Errorf("cleanup release failed: %v", err)
	}
}

// conformanceIndependentItemIDs asserts that two distinct itemIDs can be
// acquired concurrently without either call blocking or failing on the
// other's account.
func conformanceIndependentItemIDs(t *testing.T, mgr lock.Manager) {
	t.Helper()

	const itemA = "conformance-independent-a"
	const itemB = "conformance-independent-b"

	var wg sync.WaitGroup
	wg.Add(2)

	errs := make(chan error, 2)
	leases := make(chan lock.Lease, 2)

	acquire := func(itemID string) {
		defer wg.Done()
		lease, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
		if err != nil {
			errs <- fmt.Errorf("acquire %s: %w", itemID, err)
			return
		}
		leases <- lease
	}

	go acquire(itemA)
	go acquire(itemB)
	wg.Wait()
	close(errs)
	close(leases)

	for err := range errs {
		t.Errorf("unexpected error: %v", err)
	}

	for lease := range leases {
		if err := lease.Release(context.Background()); err != nil {
			t.Errorf("failed to release %s: %v", lease.ID(), err)
		}
	}
}

// conformanceKeepAliveOnValidLease asserts that calling KeepAlive on a
// lease that is still the current holder of its itemID returns nil.
func conformanceKeepAliveOnValidLease(t *testing.T, mgr lock.Manager) {
	t.Helper()
	const itemID = "conformance-keepalive"

	lease, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer func() {
		if err := lease.Release(context.Background()); err != nil {
			t.Errorf("cleanup release failed: %v", err)
		}
	}()

	// Both adapters document ID as returning the itemID it was acquired
	// for; exercise that here so the conformance suite covers it on every
	// implementation, not just whichever one happens to test it directly.
	if lease.ID() != itemID {
		t.Errorf("expected lease ID %q, got %q", itemID, lease.ID())
	}

	if err := lease.KeepAlive(context.Background()); err != nil {
		t.Errorf("keepalive on a live lease returned an error: %v", err)
	}
}

// conformanceSharedHoldersConcurrent asserts that several concurrent
// ModeShared Acquire calls against the same itemID all succeed, proving
// shared mode genuinely admits more than one holder at once (PLAN.md
// Section 13's lock granularity table: "Shared... Other shared locks").
func conformanceSharedHoldersConcurrent(t *testing.T, mgr lock.Manager) {
	t.Helper()
	const numHolders = 10
	const itemID = "conformance-shared-concurrent"

	var wg sync.WaitGroup
	wg.Add(numHolders)

	leases := make(chan lock.Lease, numHolders)
	errs := make(chan error, numHolders)

	for i := 0; i < numHolders; i++ {
		go func() {
			defer wg.Done()
			lease, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
			if err != nil {
				errs <- err
				return
			}
			leases <- lease
		}()
	}
	wg.Wait()
	close(leases)
	close(errs)

	for err := range errs {
		t.Errorf("unexpected error acquiring a shared holder: %v", err)
	}

	count := 0
	for lease := range leases {
		count++
		if err := lease.Release(context.Background()); err != nil {
			t.Errorf("failed to release shared holder: %v", err)
		}
	}
	if count != numHolders {
		t.Fatalf("expected all %d concurrent shared acquires to succeed, got %d", numHolders, count)
	}
}

// conformanceSharedBlocksExclusiveAndViceVersa asserts the other two rows of
// PLAN.md Section 13's lock granularity table: a live shared holder blocks
// a contending exclusive request, and a live exclusive holder blocks a
// contending shared request.
func conformanceSharedBlocksExclusiveAndViceVersa(t *testing.T, mgr lock.Manager) {
	t.Helper()

	t.Run("shared blocks exclusive", func(t *testing.T) {
		const itemID = "conformance-shared-blocks-exclusive"
		shared, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
		if err != nil {
			t.Fatalf("shared acquire failed: %v", err)
		}
		defer func() { _ = shared.Release(context.Background()) }()

		if _, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeExclusive}); !errors.Is(err, lock.ErrLockHeld) {
			t.Fatalf("expected ErrLockHeld for exclusive against a live shared holder, got %v", err)
		}
	})

	t.Run("exclusive blocks shared", func(t *testing.T) {
		const itemID = "conformance-exclusive-blocks-shared"
		exclusive, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
		if err != nil {
			t.Fatalf("exclusive acquire failed: %v", err)
		}
		defer func() { _ = exclusive.Release(context.Background()) }()

		if _, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared}); !errors.Is(err, lock.ErrLockHeld) {
			t.Fatalf("expected ErrLockHeld for shared against a live exclusive holder, got %v", err)
		}
	})
}

// conformanceQueuePolicyWaitsThenSucceeds asserts that a PolicyQueue Acquire
// blocks while itemID is held and succeeds once the holder releases it,
// bounded by the calling ctx rather than by a fixed internal cap.
func conformanceQueuePolicyWaitsThenSucceeds(t *testing.T, mgr lock.Manager) {
	t.Helper()
	const itemID = "conformance-queue-waits"

	holder, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("initial acquire failed: %v", err)
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := holder.Release(context.Background()); err != nil {
			t.Errorf("failed to release holder: %v", err)
		}
		close(released)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	waiter, err := mgr.Acquire(ctx, itemID, 5*time.Second, lock.AcquireOptions{Policy: lock.PolicyQueue})
	if err != nil {
		t.Fatalf("queue-policy acquire failed: %v", err)
	}
	<-released // the goroutine above must have already released by the time we got here
	if err := waiter.Release(context.Background()); err != nil {
		t.Errorf("failed to release waiter: %v", err)
	}
}

// conformanceAcquireAllReleasesOnPartialFailure asserts that lock.AcquireAll
// releases every lease it already acquired in the same call when a later
// itemID in the batch is contended, so a caller never ends up holding only
// part of the requested set (PLAN.md Section 13's "all-at-plan-time"
// acquisition strategy).
func conformanceAcquireAllReleasesOnPartialFailure(t *testing.T, mgr lock.Manager) {
	t.Helper()
	const itemA = "conformance-acquireall-a"
	const itemB = "conformance-acquireall-b"
	const itemC = "conformance-acquireall-c"

	rival, err := mgr.Acquire(context.Background(), itemB, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("setup: failed to pre-lock %s: %v", itemB, err)
	}
	defer func() { _ = rival.Release(context.Background()) }()

	_, err = lock.AcquireAll(context.Background(), mgr, []string{itemA, itemB, itemC}, 5*time.Second, lock.AcquireOptions{})
	if !errors.Is(err, lock.ErrLockHeld) {
		t.Fatalf("expected AcquireAll to fail with ErrLockHeld, got %v", err)
	}

	// itemA must have been released by AcquireAll's own rollback: prove it
	// by acquiring it fresh here. itemC was never reached (itemB failed
	// first), so it needs no such proof.
	freed, err := mgr.Acquire(context.Background(), itemA, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("expected %s to have been released by AcquireAll's rollback, got %v", itemA, err)
	}
	if err := freed.Release(context.Background()); err != nil {
		t.Errorf("cleanup release failed: %v", err)
	}
}

// conformanceSharedHoldersChurn runs sustained, overlapping shared
// acquire/keepalive/release churn from several goroutines against the same
// itemID, unlike conformanceSharedHoldersConcurrent's single simultaneous
// join. This is a real, adversarial concurrency stress case (AGENTS.md's
// Bulletproof Testing Matrix), not just a coverage exercise: it is exactly
// the shape that forces each adapter's own CAS-retry loop (see
// FAILURE_PATTERNS.md #34) to actually retry for real, since several
// goroutines join, renew, and leave the same shared entry while others are
// mid-CAS against it.
func conformanceSharedHoldersChurn(t *testing.T, mgr lock.Manager) {
	t.Helper()
	const numWorkers = 24
	const roundsPerWorker = 6
	const itemID = "conformance-shared-churn"

	// A starting gate maximizes real overlap between workers' own
	// Acquire/KeepAlive/Release calls, so their CAS operations against
	// the same itemID actually collide instead of serializing by luck.
	var startingGate sync.WaitGroup
	startingGate.Add(1)

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	errs := make(chan error, numWorkers*roundsPerWorker)

	for w := 0; w < numWorkers; w++ {
		go func() {
			defer wg.Done()
			startingGate.Wait()
			for r := 0; r < roundsPerWorker; r++ {
				lease, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
				if err != nil {
					errs <- fmt.Errorf("acquire: %w", err)
					continue
				}
				if err := lease.KeepAlive(context.Background()); err != nil {
					errs <- fmt.Errorf("keepalive: %w", err)
				}
				if err := lease.KeepAlive(context.Background()); err != nil {
					errs <- fmt.Errorf("second keepalive: %w", err)
				}
				if err := lease.Release(context.Background()); err != nil {
					errs <- fmt.Errorf("release: %w", err)
				}
			}
		}()
	}
	startingGate.Done()
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("unexpected error during shared holder churn: %v", err)
	}
}

// conformancePriorityPolicyWaitsThenSucceeds mirrors
// conformanceQueuePolicyWaitsThenSucceeds but under PolicyPriority: the
// waiting caller's own backoff cadence differs (see queue.go's own doc
// comment on contentionBackoff), but the eventual-success contract is
// identical, so this proves PolicyPriority genuinely retries rather than
// behaving like PolicyReject under contention.
func conformancePriorityPolicyWaitsThenSucceeds(t *testing.T, mgr lock.Manager) {
	t.Helper()
	const itemID = "conformance-priority-waits"

	holder, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("initial acquire failed: %v", err)
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := holder.Release(context.Background()); err != nil {
			t.Errorf("failed to release holder: %v", err)
		}
		close(released)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	waiter, err := mgr.Acquire(ctx, itemID, 5*time.Second, lock.AcquireOptions{Policy: lock.PolicyPriority, Priority: 5})
	if err != nil {
		t.Fatalf("priority-policy acquire failed: %v", err)
	}
	<-released
	if err := waiter.Release(context.Background()); err != nil {
		t.Errorf("failed to release waiter: %v", err)
	}
}

// conformanceContentionPolicyRespectsContextDeadline asserts that a
// PolicyQueue (and PolicyPriority) Acquire against a holder that never
// releases gives up once ctx's own deadline passes, returning ctx.Err(),
// rather than waiting forever.
func conformanceContentionPolicyRespectsContextDeadline(t *testing.T, mgr lock.Manager) {
	t.Helper()

	for _, policy := range []lock.ContentionPolicy{lock.PolicyQueue, lock.PolicyPriority} {
		t.Run(policy.String(), func(t *testing.T) {
			itemID := "conformance-ctx-deadline-" + policy.String()

			holder, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{})
			if err != nil {
				t.Fatalf("initial acquire failed: %v", err)
			}
			defer func() { _ = holder.Release(context.Background()) }()

			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()

			_, err = mgr.Acquire(ctx, itemID, 5*time.Second, lock.AcquireOptions{Policy: policy, Priority: 5})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected context.DeadlineExceeded once the deadline passed while still waiting, got %v", err)
			}
		})
	}
}
