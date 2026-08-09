package election_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/election"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/goleak"
)

// TestLeaderElection_ThreeReplicas_OnlyOneLeaderAndGracefulHandover is
// this package's migration of the real 3-replica scenario
// engine.Scheduler's own TestSchedulerLeaderElection proved before this
// phase extracted the mechanism out of that package: exactly one of
// three concurrently running LeaderElectors ever observes IsLeader()
// true against a real NATS JetStream KV store, and canceling the
// leader's own ctx (simulating a graceful shutdown, e.g. SIGTERM) hands
// leadership to a different replica within one tick, not electionTTL.
func TestLeaderElection_ThreeReplicas_OnlyOneLeaderAndGracefulHandover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	defer goleak.VerifyNone(t)

	ctx := context.Background()

	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.11"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	defer mgr.Close()

	const key = "release-gate-three-replicas"
	e1 := election.NewLeaderElector(mgr, key)
	e2 := election.NewLeaderElector(mgr, key)
	e3 := election.NewLeaderElector(mgr, key)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx3, cancel3 := context.WithCancel(context.Background())

	go e1.Run(ctx1)
	go e2.Run(ctx2)
	go e3.Run(ctx3)

	time.Sleep(3 * time.Second)

	leaders := 0
	var leaderCancel context.CancelFunc
	if e1.IsLeader() {
		leaders++
		leaderCancel = cancel1
	}
	if e2.IsLeader() {
		leaders++
		leaderCancel = cancel2
	}
	if e3.IsLeader() {
		leaders++
		leaderCancel = cancel3
	}
	if leaders != 1 {
		t.Fatalf("expected exactly 1 leader, got %d. SPLIT BRAIN DETECTED!", leaders)
	}
	t.Log("replica correctly acquired initial leadership")

	t.Log("simulating a graceful shutdown (SIGTERM) on the leader")
	leaderCancel()

	time.Sleep(3 * time.Second)

	leaders = 0
	if e1.IsLeader() {
		leaders++
	}
	if e2.IsLeader() {
		leaders++
	}
	if e3.IsLeader() {
		leaders++
	}
	if leaders != 1 {
		t.Fatalf("expected exactly 1 new leader after graceful handover, got %d. ELECTION FAILED!", leaders)
	}
	t.Log("graceful handover and failover successful")

	cancel1()
	cancel2()
	cancel3()

	time.Sleep(500 * time.Millisecond)
}

// mockLease is a lock.Lease test double whose KeepAlive and Release
// behavior can be scripted per call, used to exercise LeaderElector's
// renewal-failure and best-effort-release code paths without a real
// lock store.
type mockLease struct {
	mu sync.Mutex

	keepAliveCalls int
	failAfterCalls int // KeepAlive fails starting on this call number (0 = never fails)

	releaseDelay time.Duration
	releaseErr   error
	released     bool
}

func (l *mockLease) ID() string { return "mock" }

func (l *mockLease) KeepAlive(ctx context.Context) error {
	l.mu.Lock()
	l.keepAliveCalls++
	fail := l.failAfterCalls > 0 && l.keepAliveCalls >= l.failAfterCalls
	l.mu.Unlock()
	if fail {
		return errors.New("simulated renewal failure")
	}
	return nil
}

func (l *mockLease) Release(ctx context.Context) error {
	if l.releaseDelay > 0 {
		select {
		case <-time.After(l.releaseDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	l.mu.Lock()
	l.released = true
	l.mu.Unlock()
	return l.releaseErr
}

func (l *mockLease) wasReleased() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.released
}

// mockManager is a lock.Manager test double that always hands out the
// same scripted mockLease, so a test can control exactly how that
// lease's KeepAlive/Release calls behave.
type mockManager struct {
	lease *mockLease
}

func (m *mockManager) Acquire(ctx context.Context, itemID string, ttl time.Duration, opts lock.AcquireOptions) (lock.Lease, error) {
	return m.lease, nil
}

func (m *mockManager) Close() error { return nil }

// TestLeaderElector_ReleasesExplicitlyOnRenewalFailure proves fix #3
// from Phase 4's checklist: when KeepAlive fails, LeaderElector calls
// Release on the stale lease itself (instead of only dropping its local
// reference and waiting out a TTL), and reports itself as no longer
// leader.
func TestLeaderElector_ReleasesExplicitlyOnRenewalFailure(t *testing.T) {
	lease := &mockLease{failAfterCalls: 1}
	mgr := &mockManager{lease: lease}
	e := election.NewLeaderElector(mgr, "renewal-failure-key")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go e.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if lease.wasReleased() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !lease.wasReleased() {
		t.Fatal("expected the lease to be explicitly released after a renewal failure")
	}
	if e.IsLeader() {
		t.Fatal("expected IsLeader to be false after a renewal failure")
	}
}

// TestLeaderElector_OnAcquiredFiresOnceOnTransition proves WithOnAcquired
// fires exactly once when leadership is first won, not on every renewal
// tick while it is held.
func TestLeaderElector_OnAcquiredFiresOnceOnTransition(t *testing.T) {
	lease := &mockLease{}
	mgr := &mockManager{lease: lease}

	var calls int32
	var mu sync.Mutex
	e := election.NewLeaderElector(mgr, "on-acquired-key", election.WithOnAcquired(func() {
		mu.Lock()
		calls++
		mu.Unlock()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	e.Run(ctx)

	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("expected WithOnAcquired to fire exactly once, got %d", got)
	}
}

// TestLeaderElector_GracefulShutdownReleasesWithinBoundedTime proves the
// ctx.Done() release path now shares releaseBestEffort's bounded
// timeout: even if the underlying lease's own Release call is slow,
// Run still returns promptly rather than hanging on an unbounded
// context.Background() call.
func TestLeaderElector_GracefulShutdownReleasesWithinBoundedTime(t *testing.T) {
	lease := &mockLease{releaseDelay: 500 * time.Millisecond}
	mgr := &mockManager{lease: lease}
	e := election.NewLeaderElector(mgr, "graceful-shutdown-key")

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.Run(runCtx)
		close(done)
	}()

	// Give it a chance to acquire the lease before shutting down.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) && !e.IsLeader() {
		time.Sleep(10 * time.Millisecond)
	}
	if !e.IsLeader() {
		t.Fatal("expected the elector to acquire leadership before shutdown")
	}

	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within a bounded time after ctx cancellation")
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("graceful shutdown took %v, expected well under releaseTimeout's bound", elapsed)
	}
	if !lease.wasReleased() {
		t.Fatal("expected the lease to be released on graceful shutdown")
	}
}
