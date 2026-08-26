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
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
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

// TestNatsLockKeyIsASingleSubjectToken is FAILURE_PATTERNS.md #206's
// regression test, pinned against the real broker rather than a reading of
// the encoder, and falsifiable in both directions:
//
//   - Against the pre-encoder code it fails at the raw-key assertion,
//     because a dotted itemID was stored verbatim, producing a five-token
//     "$KV.<bucket>.router1.example.com" subject no per-device grant could
//     ever name.
//   - Against #206's failed first fix (which encoded every kv.* call and
//     left three of publishWithTTL's callers on the raw itemID) it fails
//     at the revision assertion, because KeepAlive's TTL-refresh publish
//     went to a subject nothing else read and could never satisfy its own
//     CAS expectation, so the encoded key's revision never moved.
//
// The revision assertion is the important half. "KeepAlive returned nil"
// is already conformance-covered; "KeepAlive's publish actually landed on
// the key this lock lives at" is the property whose absence let the first
// fix look exclusive-mode-clean while every TTL refresh silently died.
func TestNatsLockKeyIsASingleSubjectToken(t *testing.T) {
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

	mgr, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	// The itemID shape #206 is about: dots are legal in a KV key, so
	// nothing client-side ever rejected this; it simply stored a key a
	// single-token grant could never cover.
	const itemID = "router1.example.com"

	lease, err := mgr.Acquire(ctx, itemID, 5*time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// A second, independent client inspects what the bucket actually
	// stores, so every assertion below is about broker state rather than
	// about this package's own bookkeeping.
	inspectConn, err := natsgo.Connect(url)
	if err != nil {
		t.Fatalf("inspection connect failed: %v", err)
	}
	t.Cleanup(inspectConn.Close)
	inspectJS, err := jetstream.New(inspectConn)
	if err != nil {
		t.Fatalf("inspection jetstream failed: %v", err)
	}
	kv, err := inspectJS.KeyValue(ctx, topology.LockBucketName)
	if err != nil {
		t.Fatalf("inspection bucket bind failed: %v", err)
	}

	if _, err := kv.Get(ctx, itemID); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("the raw dotted itemID %q exists as a KV key (err=%v); the lock is not stored under its encoded key", itemID, err)
	}

	encoded := topology.SubjectToken(itemID)
	if strings.Contains(encoded, ".") {
		t.Fatalf("topology.SubjectToken(%q) = %q still contains a dot", itemID, encoded)
	}
	before, err := kv.Get(ctx, encoded)
	if err != nil {
		t.Fatalf("the lock's encoded key %q is not in the bucket: %v", encoded, err)
	}

	// The missed-site assertion: KeepAlive's TTL-refresh publish must
	// land on the encoded key, observable as its revision advancing.
	if err := lease.KeepAlive(ctx); err != nil {
		t.Fatalf("keepalive failed: %v", err)
	}
	after, err := kv.Get(ctx, encoded)
	if err != nil {
		t.Fatalf("the encoded key %q vanished across a KeepAlive: %v", encoded, err)
	}
	if after.Revision() <= before.Revision() {
		t.Fatalf("KeepAlive did not advance the encoded key's revision (%d -> %d); its refresh publish landed somewhere else", before.Revision(), after.Revision())
	}

	if err := lease.Release(ctx); err != nil {
		t.Fatalf("release failed: %v", err)
	}
	if _, err := kv.Get(ctx, encoded); !errors.Is(err, jetstream.ErrKeyNotFound) {
		t.Fatalf("the encoded key %q survived its release (err=%v)", encoded, err)
	}
}

// TestNatsLockCorruptedValueFailsCleanly pins the adapter's behavior when
// the stored lockValue is not JSON at all: every shared-mode path that
// reads it back (a join, KeepAlive, Release) must surface a clean decode
// error rather than panicking or, worse, treating garbage as an empty
// holder list and "succeeding". The corruption is written through a
// second, independent client straight at the ENCODED key, which is also
// one more place the FAILURE_PATTERNS.md #206 key split is exercised from
// outside the package.
func TestNatsLockCorruptedValueFailsCleanly(t *testing.T) {
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

	mgr, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	const itemID = "corrupted-value"
	lease, err := mgr.Acquire(ctx, itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	corruptConn, err := natsgo.Connect(url)
	if err != nil {
		t.Fatalf("corruption connect failed: %v", err)
	}
	t.Cleanup(corruptConn.Close)
	corruptJS, err := jetstream.New(corruptConn)
	if err != nil {
		t.Fatalf("corruption jetstream failed: %v", err)
	}
	kv, err := corruptJS.KeyValue(ctx, topology.LockBucketName)
	if err != nil {
		t.Fatalf("corruption bucket bind failed: %v", err)
	}
	if _, err := kv.Put(ctx, topology.SubjectToken(itemID), []byte("not json {")); err != nil {
		t.Fatalf("corrupting the stored value failed: %v", err)
	}

	if err := lease.KeepAlive(ctx); err == nil {
		t.Fatal("KeepAlive over a corrupted stored value returned nil")
	}
	if _, err := mgr.Acquire(ctx, itemID, 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared}); err == nil {
		t.Fatal("a shared join over a corrupted stored value returned nil")
	}
	if err := lease.Release(ctx); err == nil {
		t.Fatal("Release over a corrupted stored value returned nil")
	}
}
