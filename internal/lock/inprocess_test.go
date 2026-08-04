package lock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
)

// TestInProcessManagerBasicContract table-drives the basic single-manager
// behaviors that do not need a second lock.Manager implementation to be
// meaningful: acquiring a free itemID, colliding with a held one, freeing
// an itemID via Release, and keeping two itemIDs independent. Each case
// gets a fresh manager so cases cannot leak state into each other.
func TestInProcessManagerBasicContract(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, mgr lock.Manager)
	}{
		{
			name: "acquire on a free itemID succeeds",
			run: func(t *testing.T, mgr lock.Manager) {
				lease, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if lease.ID() != "device-1" {
					t.Errorf("expected lease ID 'device-1', got %q", lease.ID())
				}
			},
		},
		{
			name: "acquire twice on the same itemID without releasing returns ErrLockHeld",
			run: func(t *testing.T, mgr lock.Manager) {
				if _, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{}); err != nil {
					t.Fatalf("first acquire failed: %v", err)
				}
				// Mutation coverage: this catches isExpired ever being
				// forced to unconditionally report true (which would make
				// a live lock look abandoned). Verified during
				// development by hardcoding isExpired to "return true":
				// this case failed as expected, since the second Acquire
				// wrongly succeeded instead of returning ErrLockHeld.
				_, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
				if !errors.Is(err, lock.ErrLockHeld) {
					t.Fatalf("expected ErrLockHeld, got %v", err)
				}
			},
		},
		{
			name: "release then acquire again succeeds",
			run: func(t *testing.T, mgr lock.Manager) {
				lease, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
				if err != nil {
					t.Fatalf("first acquire failed: %v", err)
				}
				if err := lease.Release(context.Background()); err != nil {
					t.Fatalf("release failed: %v", err)
				}
				if _, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{}); err != nil {
					t.Fatalf("second acquire after release failed: %v", err)
				}
			},
		},
		{
			name: "two different itemIDs are independent",
			run: func(t *testing.T, mgr lock.Manager) {
				lease1, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
				if err != nil {
					t.Fatalf("acquire device-1 failed: %v", err)
				}
				lease2, err := mgr.Acquire(context.Background(), "device-2", 5*time.Second, lock.AcquireOptions{})
				if err != nil {
					t.Fatalf("acquire device-2 failed: %v", err)
				}
				if lease1.ID() == lease2.ID() {
					t.Fatalf("expected distinct lease IDs, both were %q", lease1.ID())
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := lock.NewInProcessManager()
			tc.run(t, mgr)
		})
	}
}

// TestInProcessManagerKeepAlive covers KeepAlive on both a lease that is
// still current and one that has been superseded.
func TestInProcessManagerKeepAlive(t *testing.T) {
	t.Run("keepalive on a live lease succeeds", func(t *testing.T) {
		mgr := lock.NewInProcessManager()
		lease, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}
		if err := lease.KeepAlive(context.Background()); err != nil {
			t.Errorf("keepalive on a live lease returned an error: %v", err)
		}
	})

	t.Run("keepalive on a superseded lease returns an error", func(t *testing.T) {
		mgr := lock.NewInProcessManager()
		// Use a ttl short enough that the lock has certainly expired by
		// the time we try to reclaim it below, without a long sleep.
		const shortTTL = 10 * time.Millisecond
		staleLease, err := mgr.Acquire(context.Background(), "device-1", shortTTL, lock.AcquireOptions{})
		if err != nil {
			t.Fatalf("first acquire failed: %v", err)
		}

		// Let the lock age past its deadline, then let a second caller
		// reclaim it. staleLease now refers to a token the manager no
		// longer recognizes as current.
		time.Sleep(30 * time.Millisecond)
		if _, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{}); err != nil {
			t.Fatalf("reclaiming acquire failed: %v", err)
		}

		// Mutation coverage: this catches the token comparison in
		// KeepAlive ever being dropped. Verified during development by
		// changing "!held || entry.token != l.token" to just "!held":
		// this case failed as expected, since the stale KeepAlive wrongly
		// succeeded and would have corrupted the new holder's deadline.
		if err := staleLease.KeepAlive(context.Background()); err == nil {
			t.Fatal("expected an error keeping alive a superseded lease, got nil")
		}
	})
}

// TestInProcessManagerRelease covers Release on both a lease that is still
// current and one that has been superseded.
func TestInProcessManagerRelease(t *testing.T) {
	t.Run("release on a superseded lease returns an error and does not touch the new holder", func(t *testing.T) {
		mgr := lock.NewInProcessManager()
		const shortTTL = 10 * time.Millisecond
		staleLease, err := mgr.Acquire(context.Background(), "device-1", shortTTL, lock.AcquireOptions{})
		if err != nil {
			t.Fatalf("first acquire failed: %v", err)
		}

		time.Sleep(30 * time.Millisecond)
		newLease, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
		if err != nil {
			t.Fatalf("reclaiming acquire failed: %v", err)
		}

		// Mutation coverage: this catches the token comparison in Release
		// ever being dropped. Verified during development by changing
		// "!held || entry.token != l.token" to just "!held": this case
		// failed as expected, since the stale Release wrongly deleted the
		// new holder's entry instead of returning an error.
		if err := staleLease.Release(context.Background()); err == nil {
			t.Fatal("expected an error releasing a superseded lease, got nil")
		}

		// The new holder's lease must still be releasable: proof that the
		// stale Release above did not delete it.
		if err := newLease.Release(context.Background()); err != nil {
			t.Fatalf("new holder's lease was corrupted by the stale release: %v", err)
		}
	})
}

// TestInProcessManagerTTLExpiry proves the in-process adapter honors the
// per-call ttl argument, unlike the NATS adapter (which ignores it in
// favor of one fixed bucket-wide TTL). A lock acquired with a short ttl
// must become reclaimable once that ttl elapses.
func TestInProcessManagerTTLExpiry(t *testing.T) {
	mgr := lock.NewInProcessManager()
	const shortTTL = 10 * time.Millisecond

	if _, err := mgr.Acquire(context.Background(), "device-1", shortTTL, lock.AcquireOptions{}); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// Confirm the lock is genuinely held before it has had time to expire.
	if _, err := mgr.Acquire(context.Background(), "device-1", shortTTL, lock.AcquireOptions{}); !errors.Is(err, lock.ErrLockHeld) {
		t.Fatalf("expected ErrLockHeld immediately after acquire, got %v", err)
	}

	// Real sleep kept in the tens-of-milliseconds range so the suite stays
	// fast: 3x the ttl gives comfortable margin against scheduler jitter
	// without risking a slow, flaky multi-second wait.
	time.Sleep(3 * shortTTL)

	if _, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{}); err != nil {
		t.Fatalf("expected the expired lock to be reclaimable, got %v", err)
	}
}

// TestInProcessManagerContextCancellation asserts that Acquire, KeepAlive,
// and Release all return ctx.Err() promptly when called with an
// already-canceled context, since there is no blocking I/O to select on
// beyond that check.
func TestInProcessManagerContextCancellation(t *testing.T) {
	mgr := lock.NewInProcessManager()

	// Acquire a live lease first so KeepAlive and Release below have
	// something real to operate on.
	lease, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("setup acquire failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before any call below observes ctx

	if _, err := mgr.Acquire(ctx, "device-2", 5*time.Second, lock.AcquireOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Acquire: expected context.Canceled, got %v", err)
	}
	if err := lease.KeepAlive(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("KeepAlive: expected context.Canceled, got %v", err)
	}
	if err := lease.Release(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Release: expected context.Canceled, got %v", err)
	}
}

// TestInProcessManagerKeepAliveAndReleaseAfterEntryFullyGone covers the
// branch where itemID's entry has been removed from the manager entirely
// (every holder released, not just superseded by a different token): a
// distinct case from the "superseded lease" scenario
// TestInProcessManagerKeepAlive/TestInProcessManagerRelease already cover,
// where an entry still exists but under a different token.
func TestInProcessManagerKeepAliveAndReleaseAfterEntryFullyGone(t *testing.T) {
	mgr := lock.NewInProcessManager()
	lease, err := mgr.Acquire(context.Background(), "device-1", 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	// The map entry for "device-1" no longer exists at all now (the only
	// holder just released it), not merely superseded by a new token.

	if err := lease.KeepAlive(context.Background()); err == nil {
		t.Fatal("expected an error keeping alive a lease whose entry no longer exists at all, got nil")
	}
	if err := lease.Release(context.Background()); err == nil {
		t.Fatal("expected an error releasing a lease whose entry no longer exists at all, got nil")
	}
}

// TestInProcessManagerClose asserts that Close is a harmless no-op.
func TestInProcessManagerClose(t *testing.T) {
	mgr := lock.NewInProcessManager()
	if err := mgr.Close(); err != nil {
		t.Errorf("expected Close to return nil, got %v", err)
	}
}

// TestInProcessManagerConformance runs the shared adapter conformance
// suite against inProcessManager, using a fresh manager for every subtest.
func TestInProcessManagerConformance(t *testing.T) {
	runManagerConformance(t, func() lock.Manager {
		return lock.NewInProcessManager()
	})
}
