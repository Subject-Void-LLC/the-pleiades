package election_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/election"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
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

	const key = "release-gate-three-replicas"
	e1 := election.NewLeaderElector(mgr, key)
	e2 := election.NewLeaderElector(mgr, key)
	e3 := election.NewLeaderElector(mgr, key)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx3, cancel3 := context.WithCancel(context.Background())

	electors := []*election.LeaderElector{e1, e2, e3}
	cancels := []context.CancelFunc{cancel1, cancel2, cancel3}

	go e1.Run(ctx1)
	go e2.Run(ctx2)
	go e3.Run(ctx3)

	first := awaitSingleLeader(t, "initial election", electors, noExclusion)
	t.Log("replica correctly acquired initial leadership")

	t.Log("simulating a graceful shutdown (SIGTERM) on the leader")
	cancels[first]()

	second := awaitSingleLeader(t, "graceful handover", electors, first)
	if second == first {
		t.Fatal("awaitSingleLeader returned the replica it was told to exclude")
	}
	t.Log("graceful handover and failover successful")

	cancel1()
	cancel2()
	cancel3()

	time.Sleep(500 * time.Millisecond)
}

// noExclusion is awaitSingleLeader's "any replica will do" argument, used
// for the initial election where no replica is stepping down.
const noExclusion = -1

// leaderPollInterval and leaderPollTimeout drive awaitSingleLeader. The
// timeout is deliberately generous, because it bounds a LIVENESS wait:
// making it long costs only how long a genuinely broken run takes to
// fail, while making it tight costs a false failure every time this
// package shares a machine with the rest of the suite.
const (
	leaderPollInterval = 50 * time.Millisecond
	leaderPollTimeout  = 20 * time.Second
)

// awaitSingleLeader polls electors until exactly one reports leadership
// and returns its index. A non-negative excluding names a replica that is
// stepping down, whose own leadership does not count as the answer.
//
// It separates two properties this test conflated until 2026-09-15, when
// internal/election was the one genuinely production-relevant entry in
// flaky-packages.json:
//
//   - SAFETY, that no two replicas ever lead at once, is checked at every
//     sample. That is strictly STRONGER than what this test did before,
//     which was to sleep three seconds and sample once, and so could not
//     observe a transient double leader at all.
//   - LIVENESS, that somebody leads, is checked only at the deadline and
//     is reported as itself. Under full-suite parallel load a count of
//     zero means no replica has been scheduled yet, and the old code
//     announced that as "SPLIT BRAIN DETECTED!", which made the loudest
//     failure in the suite also its least accurate one.
//
// The per-sample safety check is only sound because Run clears isLeader
// BEFORE releasing the lease at each of its three step-down sites.
// Releasing first would leave a window where the outgoing leader still
// reports true and its successor has already acquired, and this check
// would report that legitimate handover as a split brain.
func awaitSingleLeader(t *testing.T, what string, electors []*election.LeaderElector, excluding int) int {
	t.Helper()

	deadline := time.Now().Add(leaderPollTimeout)
	for {
		var held []int
		for i, e := range electors {
			if e.IsLeader() {
				held = append(held, i)
			}
		}

		if len(held) > 1 {
			t.Fatalf("%s: %d of %d replicas report leadership at once (indices %v). SPLIT BRAIN DETECTED!",
				what, len(held), len(electors), held)
		}
		if len(held) == 1 && held[0] != excluding {
			return held[0]
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s: no replica took the lease within %s. This is a LIVENESS failure and NOT a split brain: "+
				"no two replicas were ever seen leading at once during the wait. Under full-suite parallel load "+
				"the usual cause is CPU starvation or a slow broker, and this package passes when run alone.",
				what, leaderPollTimeout)
		}
		time.Sleep(leaderPollInterval)
	}
}

// mockLease is a lock.Lease test double whose KeepAlive and Release
// behavior can be scripted per call, used to exercise LeaderElector's
// renewal-failure and best-effort-release code paths without a real
// lock store.
type mockLease struct {
	mu sync.Mutex

	keepAliveCalls int
	failAfterCalls int // KeepAlive fails starting on this call number (0 = never fails)

	// onKeepAliveFail runs inside KeepAlive, immediately before it
	// returns its simulated failure, so a test can make some other
	// state change land *before* Run observes that failure. Its one
	// use is canceling the elector's own ctx, which is what makes the
	// "renewal failed because we are shutting down" branch reachable
	// deterministically instead of only when a real store happens to
	// lose that race (see
	// TestLeaderElector_RenewalFailureDuringShutdownStepsDownSilently).
	onKeepAliveFail func()

	releaseDelay time.Duration
	releaseErr   error
	released     bool
}

func (l *mockLease) ID() string { return "mock" }

func (l *mockLease) KeepAlive(ctx context.Context) error {
	l.mu.Lock()
	l.keepAliveCalls++
	fail := l.failAfterCalls > 0 && l.keepAliveCalls >= l.failAfterCalls
	onFail := l.onKeepAliveFail
	l.mu.Unlock()
	if fail {
		if onFail != nil {
			onFail()
		}
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
// lease's KeepAlive/Release calls behave. acquireErr and onAcquire
// additionally let a test script the acquisition side: every branch of
// Run's own acquire switch (won, contended, shutting down, genuinely
// broken) is reachable from here without depending on a real store
// happening to produce that outcome.
type mockManager struct {
	lease *mockLease

	// acquireErr, when non-nil, is returned instead of lease, so a test
	// picks which arm of Run's acquire switch it exercises: lock.ErrLockHeld
	// for the contended steady state, any other error for the
	// store-is-broken arm.
	acquireErr error

	// onAcquire runs inside Acquire, immediately before it returns, for
	// the same reason mockLease.onKeepAliveFail exists: it lets a test
	// cancel the elector's ctx mid-call so the "acquire failed because we
	// are shutting down" arm is reached deterministically.
	onAcquire func()
}

func (m *mockManager) Acquire(ctx context.Context, itemID string, ttl time.Duration, opts lock.AcquireOptions) (lock.Lease, error) {
	if m.onAcquire != nil {
		m.onAcquire()
	}
	if m.acquireErr != nil {
		return nil, m.acquireErr
	}
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

// The four tests below exist for a reason worth stating explicitly,
// because it is not the usual one: each covers a branch of Run that was
// already being reached before they existed, but only *incidentally*,
// as a side effect of whichever way a real NATS store happened to lose
// a timing race in TestLeaderElection_ThreeReplicas above. That made
// this package's measured coverage swing between 85.0% and 97.5% from
// run to run against a fixed floor, so `make ci` failed at the coverage
// ratchet on a schedule nobody controlled and no code change caused. A
// branch reached by luck is not a tested branch: these drive each one
// through a scripted test double, so the same statements are covered on
// every run, on a loaded CI runner exactly as on an idle laptop.

// waitFor blocks until cond returns true, failing the test if that has
// not happened within timeout. It replaces the fixed-duration sleeps the
// older tests in this file use: a test that waits for the condition it
// actually cares about neither wastes time on a fast machine nor flakes
// on a slow one.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, what)
}

// waitForReturn fails the test unless Run's goroutine has already
// returned, or returns within timeout.
func waitForReturn(t *testing.T, done <-chan struct{}, timeout time.Duration, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("Run did not return within %v after %s", timeout, what)
	}
}

// TestLeaderElector_RenewalFailureToleratesAFailingRelease proves the
// best-effort release really is best-effort: when the post-renewal-
// failure Release call itself fails (the stale-revision CAS race
// releaseBestEffort's own doc comment documents as accepted), the
// elector still steps down rather than treating a failed cleanup as a
// reason to keep believing it is the leader.
func TestLeaderElector_RenewalFailureToleratesAFailingRelease(t *testing.T) {
	lease := &mockLease{
		failAfterCalls: 1,
		releaseErr:     errors.New("simulated stale-revision release failure"),
	}
	mgr := &mockManager{lease: lease}
	e := election.NewLeaderElector(mgr, "failing-release-key")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()

	waitFor(t, 5*time.Second, "the stale lease to be released", lease.wasReleased)
	waitFor(t, 5*time.Second, "the elector to step down", func() bool { return !e.IsLeader() })

	cancel()
	waitForReturn(t, done, 5*time.Second, "ctx cancellation")
}

// TestLeaderElector_RenewalFailureDuringShutdownStepsDownSilently
// covers the branch that distinguishes "the store broke" from "we are
// shutting down": a renewal that fails *because* ctx was canceled while
// the call was in flight must release, step down, and return, without
// logging the store-side failure warning that a genuine renewal failure
// earns.
func TestLeaderElector_RenewalFailureDuringShutdownStepsDownSilently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Canceling from inside KeepAlive is what makes this deterministic:
	// Run re-checks ctx.Err() the instant KeepAlive returns, so the
	// cancellation is guaranteed to already be visible by then.
	lease := &mockLease{failAfterCalls: 1, onKeepAliveFail: cancel}
	mgr := &mockManager{lease: lease}
	e := election.NewLeaderElector(mgr, "renewal-shutdown-race-key")

	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()

	waitForReturn(t, done, 5*time.Second, "a renewal failure racing with shutdown")

	if !lease.wasReleased() {
		t.Fatal("expected the lease to be released when a renewal failed during shutdown")
	}
	if e.IsLeader() {
		t.Fatal("expected IsLeader to be false after stepping down during shutdown")
	}
}

// TestLeaderElector_AcquireFailureIsLoggedAndRetried covers the acquire
// switch's default arm: an error that is neither "someone else holds it"
// nor "we are shutting down" means the lock store itself is unreachable.
// The elector must keep running and keep retrying rather than exiting or
// falsely reporting leadership.
func TestLeaderElector_AcquireFailureIsLoggedAndRetried(t *testing.T) {
	var attempts atomic.Int32
	mgr := &mockManager{
		acquireErr: errors.New("simulated unreachable lock store"),
		onAcquire:  func() { attempts.Add(1) },
	}
	e := election.NewLeaderElector(mgr, "acquire-error-key")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()

	// Two attempts, not one: the second is what proves a failed acquire
	// leaves the loop running and retrying rather than exiting.
	waitFor(t, 10*time.Second, "two failed acquire attempts", func() bool { return attempts.Load() >= 2 })

	if e.IsLeader() {
		t.Fatal("expected IsLeader to be false when every acquire attempt failed")
	}

	cancel()
	waitForReturn(t, done, 5*time.Second, "ctx cancellation")
}

// TestLeaderElector_ContendedAcquireIsNotAnError covers the acquire
// switch's lock.ErrLockHeld arm: the ordinary steady state of every
// non-leader replica. It is deliberately silent and must never be
// mistaken for a failure, so the loop keeps polling and never claims
// leadership.
func TestLeaderElector_ContendedAcquireIsNotAnError(t *testing.T) {
	var attempts atomic.Int32
	mgr := &mockManager{
		acquireErr: lock.ErrLockHeld,
		onAcquire:  func() { attempts.Add(1) },
	}
	e := election.NewLeaderElector(mgr, "contended-key")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()

	waitFor(t, 10*time.Second, "two contended acquire attempts", func() bool { return attempts.Load() >= 2 })

	if e.IsLeader() {
		t.Fatal("expected IsLeader to be false while another replica holds the lease")
	}

	cancel()
	waitForReturn(t, done, 5*time.Second, "ctx cancellation")
}

// TestLeaderElector_AcquireFailureDuringShutdownIsNotAnError covers the
// acquire switch's ctx.Err() arm, the acquisition-side twin of
// TestLeaderElector_RenewalFailureDuringShutdownStepsDownSilently: an
// acquire that fails because ctx was canceled mid-call is an ordinary
// shutdown race, not a store-side error worth logging as one.
func TestLeaderElector_AcquireFailureDuringShutdownIsNotAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := &mockManager{
		acquireErr: errors.New("simulated acquire failure racing with shutdown"),
		onAcquire:  cancel,
	}
	e := election.NewLeaderElector(mgr, "acquire-shutdown-race-key")

	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()

	waitForReturn(t, done, 5*time.Second, "an acquire failure racing with shutdown")

	if e.IsLeader() {
		t.Fatal("expected IsLeader to be false when acquisition never succeeded")
	}
}
