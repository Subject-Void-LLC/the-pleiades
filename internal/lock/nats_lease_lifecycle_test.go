package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newBenchOrTestNatsManager starts a real, dedicated NATS container and
// returns a ready lock.Manager plus a cleanup-registering *testing.T. Each
// test in this file wants its own container rather than sharing one
// (unlike TestNatsManagerConformance's subtests), since several
// deliberately leave a lease or itemID in a broken/exhausted state.
func newBenchOrTestNatsManager(t *testing.T) lock.Manager {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	t.Cleanup(func() { _ = natsContainer.Terminate(ctx) })

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	return mgr
}

// TestNatsLease_KeepAliveContextAlreadyCanceled asserts KeepAlive returns
// ctx's own error immediately, with no network call, given an
// already-canceled context.
func TestNatsLease_KeepAliveContextAlreadyCanceled(t *testing.T) {
	mgr := newBenchOrTestNatsManager(t)

	lease, err := mgr.Acquire(context.Background(), "keepalive-ctx-canceled", 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer func() { _ = lease.Release(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := lease.KeepAlive(ctx); err == nil {
		t.Fatal("expected an error from KeepAlive with an already-canceled context, got nil")
	}
}

// TestNatsLease_KeepAliveAfterLocalDeadlinePassed asserts KeepAlive
// self-fences once this process's own locally-tracked deadline has passed,
// the concrete behavior behind Section 16's monotonic-time clock-drift
// mitigation (nats.go's own doc comment on natsLease.deadline).
func TestNatsLease_KeepAliveAfterLocalDeadlinePassed(t *testing.T) {
	mgr := newBenchOrTestNatsManager(t)

	// One second is the real minimum positive ttl this adapter accepts
	// (nats.go's own minPositiveTTL); sleeping past it lets the lease's
	// own local deadline lapse without needing a sub-second ttl.
	lease, err := mgr.Acquire(context.Background(), "keepalive-local-deadline", time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	if err := lease.KeepAlive(context.Background()); err == nil {
		t.Fatal("expected KeepAlive to self-fence once its own local deadline passed, got nil")
	}
}

// TestNatsLease_ExclusiveKeepAliveAfterRelease asserts KeepAlive on an
// exclusive lease that has already been released (so its own tracked
// revision is stale) fails via the same CAS mismatch Release itself would
// hit, rather than silently republishing a value nothing else references.
func TestNatsLease_ExclusiveKeepAliveAfterRelease(t *testing.T) {
	mgr := newBenchOrTestNatsManager(t)

	lease, err := mgr.Acquire(context.Background(), "exclusive-keepalive-after-release", 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	if err := lease.KeepAlive(context.Background()); err == nil {
		t.Fatal("expected KeepAlive on an already-released exclusive lease to fail, got nil")
	}
}

// TestNatsLease_ExclusiveReleaseTwice asserts a second Release on an
// already-released exclusive lease fails (its tracked revision is stale),
// rather than silently succeeding or corrupting whatever a later Acquire
// on the same itemID has since created.
func TestNatsLease_ExclusiveReleaseTwice(t *testing.T) {
	mgr := newBenchOrTestNatsManager(t)

	lease, err := mgr.Acquire(context.Background(), "exclusive-release-twice", 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("first release failed: %v", err)
	}

	if err := lease.Release(context.Background()); err == nil {
		t.Fatal("expected a second release of an already-released exclusive lease to fail, got nil")
	}
}

// TestNatsLease_SharedKeepAliveAndReleaseAfterEntryFullyGone covers both
// KeepAlive and Release's own shared-mode loop when itemID's entry has
// been removed entirely (every holder released, not superseded by a
// different holder still using the same key): kv.Get itself fails with
// ErrKeyNotFound, a distinct case from a still-existing entry that simply
// no longer lists this lease's own holderID (see the next test).
func TestNatsLease_SharedKeepAliveAndReleaseAfterEntryFullyGone(t *testing.T) {
	mgr := newBenchOrTestNatsManager(t)

	lease, err := mgr.Acquire(context.Background(), "shared-fully-gone", 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	// itemID's entry no longer exists at all now (the only holder just
	// released it).

	if err := lease.KeepAlive(context.Background()); err == nil {
		t.Fatal("expected KeepAlive to fail once the shared entry is fully gone, got nil")
	}
	if err := lease.Release(context.Background()); err == nil {
		t.Fatal("expected a second release to fail once the shared entry is fully gone, got nil")
	}
}

// TestNatsLease_SharedKeepAliveAndReleaseAfterSupersededByFreshHolder
// covers the branch, in both KeepAlive and Release, where itemID's entry
// exists (kv.Get succeeds) but this stale lease's own holderID is no
// longer in it: the original holder released, and a completely different
// Acquire call created a fresh entry for the same itemID in the meantime.
func TestNatsLease_SharedKeepAliveAndReleaseAfterSupersededByFreshHolder(t *testing.T) {
	mgr := newBenchOrTestNatsManager(t)
	const itemID = "shared-superseded-by-fresh-holder"

	staleForKeepAlive, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	staleForRelease, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
	if err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}
	if err := staleForKeepAlive.Release(context.Background()); err != nil {
		t.Fatalf("release of staleForKeepAlive failed: %v", err)
	}
	if err := staleForRelease.Release(context.Background()); err != nil {
		t.Fatalf("release of staleForRelease failed: %v", err)
	}

	fresh, err := mgr.Acquire(context.Background(), itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
	if err != nil {
		t.Fatalf("third acquire failed: %v", err)
	}
	defer func() { _ = fresh.Release(context.Background()) }()

	if err := staleForKeepAlive.KeepAlive(context.Background()); err == nil {
		t.Fatal("expected KeepAlive on a lease superseded by a completely fresh holder to fail, got nil")
	}
	if err := staleForRelease.Release(context.Background()); err == nil {
		t.Fatal("expected Release on a lease superseded by a completely fresh holder to fail, got nil")
	}
}
