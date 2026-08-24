package lock_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestThunderingHerdLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// 1. Spin up ephemeral NATS container
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// 2. Initialize Lock Manager
	mgr, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}

	// 3. The Thundering Herd
	// We spawn 100 goroutines that will all attempt to acquire the lock at the exact same moment.
	numRoutines := 100
	var wg sync.WaitGroup
	wg.Add(numRoutines)

	// WaitGroup to hold them at the starting gate so they fire simultaneously
	var startingGate sync.WaitGroup
	startingGate.Add(1)

	var successCount int32
	var lockedCount int32
	var unexpectedErrors int32

	targetDevice := "core-router-01"

	for i := 0; i < numRoutines; i++ {
		go func() {
			defer wg.Done()
			startingGate.Wait() // Block until the starting gun fires

			// Attempt lock
			lease, err := mgr.Acquire(context.Background(), targetDevice, 5*time.Second, lock.AcquireOptions{})

			if err == nil {
				// We won the race!
				atomic.AddInt32(&successCount, 1)

				// Keep the lock for a moment to ensure no one else gets it
				time.Sleep(50 * time.Millisecond)

				// Release it (cleanup)
				if err := lease.Release(context.Background()); err != nil {
					t.Errorf("failed to release lock: %v", err)
				}
			} else if err == lock.ErrLockHeld {
				// We lost the race, which is expected for 99 of the routines
				atomic.AddInt32(&lockedCount, 1)
			} else {
				// Some other transport or KV error occurred
				atomic.AddInt32(&unexpectedErrors, 1)
				t.Errorf("unexpected error during acquire: %v", err)
			}
		}()
	}

	// Release the hounds!
	startingGate.Done()

	// Wait for all 100 routines to finish
	wg.Wait()

	// 4. Validate the Release Gate
	if unexpectedErrors > 0 {
		t.Fatalf("Encountered %d unexpected errors during thundering herd", unexpectedErrors)
	}

	if successCount != 1 {
		t.Fatalf("Expected exactly 1 routine to acquire the lock, but %d succeeded. SPLIT BRAIN DETECTED!", successCount)
	}

	if lockedCount != 99 {
		t.Fatalf("Expected exactly 99 routines to hit ErrLockHeld, got %d", lockedCount)
	}

	t.Log("Thundering Herd Test Passed! Lock exclusivity is perfectly maintained under massive concurrency.")
}

// TestNewNatsLockManagerConnectError asserts that NewNatsLockManager
// surfaces a wrapped error instead of panicking or hanging when it cannot
// reach a broker.
//
// This test's premise has now moved twice, which is worth recording
// because the moves were both improvements and both silent. It originally
// said it exercised "the connect-failure branch", on the reasoning that a
// malformed URL fails address resolution near-instantly. Phase 96a's
// RetryOnFailedConnect made nats.Connect return nil for this exact URL in
// 310 microseconds and retry in the background, so the branch it actually
// reached became the bounded connect wait, ten seconds later. Phase 96d
// then added scheme validation ahead of the dial, so the failure is once
// again immediate, and now comes from the place that can give the operator
// a useful message.
//
// The assertion follows the behaviour rather than the other way round: a
// value with no scheme is refused before any connection is attempted,
// because nats.go would otherwise treat it as plaintext and connect
// successfully to something unencrypted.
func TestNewNatsLockManagerConnectError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	_, err := lock.NewNatsLockManager(ctx, "not-a-valid-url::::", nil, topology.StreamProvisioner)
	if err == nil {
		t.Fatal("expected an error for a URL with no usable scheme, got nil")
	}
	if !strings.Contains(err.Error(), "scheme") {
		t.Errorf("error = %v, want it to explain that the URL needs a scheme", err)
	}
	// Immediate, not after the bounded connect wait: a value that can
	// never work should not cost a startup timeout to reject.
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("rejecting an unusable URL took %v; validation should precede the dial", elapsed)
	}
}

// TestNewNatsLockManagerRejectsOldServer asserts that NewNatsLockManager
// fails closed, with a clear wrapped error, against a real nats-server too
// old to support LimitMarkerTTL (this package's own real minimum version
// requirement, see nats.go's own doc comment and LESSONS_LEARNED.md),
// rather than silently constructing a Manager that can never honor a
// positive ttl. Confirmed empirically while designing this phase that
// nats:2.10 specifically rejects this bucket config; that exact version is
// used here deliberately, not a placeholder "old" tag.
func TestNewNatsLockManagerRejectsOldServer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	_, err = lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err == nil {
		t.Fatal("expected NewNatsLockManager to fail against a pre-2.11 nats-server, got nil error")
	}
}

// TestNatsLockManagerAcquireContextAlreadyCanceled asserts that Acquire
// returns ctx's own error immediately, with no network call, when given an
// already-canceled context: tryAcquireOnce's retry loop checks ctx.Err()
// at the top of every attempt, including the first.
func TestNatsLockManagerAcquireContextAlreadyCanceled(t *testing.T) {
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
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	defer mgr.Close()

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := mgr.Acquire(canceledCtx, "already-canceled", 5*time.Second, lock.AcquireOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestNatsManagerConformance runs the shared adapter conformance suite
// (internal/lock/conformance_test.go) against a real natsLockManager
// backed by an ephemeral NATS container, proving the NATS adapter honors
// the same substitutable Manager contract as inProcessManager.
func TestNatsManagerConformance(t *testing.T) {
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
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	defer mgr.Close()

	// The same already-connected manager is handed back on every call.
	// Each conformance subtest uses itemIDs unique to itself, so sharing
	// one manager (and thus one underlying KV bucket) across subtests is
	// safe.
	runManagerConformance(t, func() lock.Manager {
		return mgr
	})
}
