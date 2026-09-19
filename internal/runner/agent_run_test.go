package runner_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// blockingAdapter is a runner.ExecutionAdapter whose Execute call blocks
// on release until it is closed (or ctx is canceled first), tracking how
// many calls are simultaneously blocked in inFlight/peak. It exists to
// prove Agent's own worker pool (agent_run.go) actually runs handleMessage
// calls concurrently, not merely "without panicking": a serial-only
// implementation could never let more than one call be in flight at once.
type blockingAdapter struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	release  chan struct{}
}

func newBlockingAdapter() *blockingAdapter {
	return &blockingAdapter{release: make(chan struct{})}
}

func (a *blockingAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	n := a.inFlight.Add(1)
	defer a.inFlight.Add(-1)
	for {
		p := a.peak.Load()
		if n <= p {
			break
		}
		if a.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-a.release:
		return wire.Outcome{}, nil
	case <-ctx.Done():
		return wire.Outcome{}, ctx.Err()
	}
}

// TestAgent_Run_ProcessesMessagesConcurrently proves the bounded worker
// pool (WithPoolSize) genuinely runs handleMessage calls concurrently,
// not serially behind a hidden lock: poolSize distinct-device messages
// are fed in, and the test asserts blockingAdapter.peak actually reaches
// poolSize before any of them is allowed to complete. A pool that
// silently processed messages one at a time (e.g. a bug that
// re-serialized fetchLoop's own dispatch) would leave peak stuck at 1
// forever, since nothing else would ever unblock the release channel to
// let the first one return.
func TestAgent_Run_ProcessesMessagesConcurrently(t *testing.T) {
	const poolSize = 3

	var msgs []jetstream.Msg
	mockMsgs := make([]*MockMsg, poolSize)
	for i := 0; i < poolSize; i++ {
		mockMsgs[i] = &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON(fmt.Sprintf("device-%d", i)))}
		msgs = append(msgs, mockMsgs[i])
	}

	adapter := newBlockingAdapter()
	consumer := &MockConsumer{PayloadMsgs: msgs}
	agent := runner.NewAgent(consumer, adapter, nil, lock.NewInProcessManager(), 5,
		slog.New(slog.NewTextHandler(new(discardWriter), nil)), nil, runner.WithPoolSize(poolSize))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && adapter.peak.Load() < poolSize {
		time.Sleep(2 * time.Millisecond)
	}
	if got := adapter.peak.Load(); got < poolSize {
		t.Fatalf("peak concurrent Execute calls = %d, want >= %d (the pool never ran more than %d concurrently)", got, poolSize, got)
	}

	close(adapter.release)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && countAcked(mockMsgs) < poolSize {
		time.Sleep(2 * time.Millisecond)
	}
	if got := countAcked(mockMsgs); got != poolSize {
		t.Fatalf("acked = %d, want %d", got, poolSize)
	}

	cancel()
	if err := <-runErr; err != context.Canceled {
		t.Fatalf("Run() error = %v, want context.Canceled", err)
	}
}

// slowCancelAdapter simulates a real adapter's post-cancellation cleanup
// taking a real, observable amount of time: Execute does not return the
// instant ctx is canceled. It first signals started (proving it was
// genuinely entered), waits for ctx.Done(), signals cancelObserved
// (proving it saw cancellation), and only then blocks on
// proceedAfterCancel until the test releases it. This is what lets
// TestAgent_Run_GracefulShutdownDrainsInFlightWork prove Run's own
// wg.Wait() genuinely blocks on a worker's real unwind, rather than the
// test being unable to distinguish "Run waited" from "Run happened to
// return quickly for an unrelated reason."
type slowCancelAdapter struct {
	started            chan struct{}
	cancelObserved     chan struct{}
	proceedAfterCancel chan struct{}
}

func newSlowCancelAdapter() *slowCancelAdapter {
	return &slowCancelAdapter{
		started:            make(chan struct{}),
		cancelObserved:     make(chan struct{}),
		proceedAfterCancel: make(chan struct{}),
	}
}

func (a *slowCancelAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	close(a.started)
	<-ctx.Done()
	close(a.cancelObserved)
	<-a.proceedAfterCancel
	return wire.Outcome{}, ctx.Err()
}

// TestAgent_Run_GracefulShutdownDrainsInFlightWork proves Run does not
// return the instant ctx is canceled while a worker is mid-handleMessage:
// it waits for that in-flight call to actually unwind (its own
// cancellation-aware Execute observes ctx.Done() and then completes its
// own cleanup, and executeWithLease's deferred lease.Release runs)
// before Run itself returns, rather than abandoning the worker goroutine
// with no synchronization at all (PATTERNS.md's Graceful Shutdown entry:
// "releases any held resources... only then exits"). Proof that the
// lease was actually released, not merely that Run returned, is what
// would catch a real regression here: an orphaned lock held past its own
// release is exactly the split-brain risk PLAN.md Section 13 exists to
// prevent.
func TestAgent_Run_GracefulShutdownDrainsInFlightWork(t *testing.T) {
	adapter := newSlowCancelAdapter()
	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-shutdown"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	locks := lock.NewInProcessManager()
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5,
		slog.New(slog.NewTextHandler(new(discardWriter), nil)), nil, runner.WithPoolSize(1))

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}

	cancel()

	select {
	case <-adapter.cancelObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute never observed ctx cancellation")
	}

	// Run must not have returned yet: the in-flight worker has seen
	// cancellation but is still unwinding its own cleanup
	// (proceedAfterCancel not yet closed), so wg.Wait() inside Run has
	// something real left to wait for.
	select {
	case err := <-runErr:
		t.Fatalf("Run() returned (%v) before the in-flight worker finished its own post-cancellation cleanup; it did not wait for in-flight work", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(adapter.proceedAfterCancel)

	select {
	case err := <-runErr:
		if err != context.Canceled {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after the in-flight worker finished its own cleanup")
	}

	// The lease executeWithLease held for "device-shutdown" must have
	// been released as part of that unwind: a fresh Acquire for the same
	// device must succeed immediately, not block or fail with
	// lock.ErrLockHeld.
	lease, err := locks.Acquire(context.Background(), "device-shutdown", time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("device lease was not released by Run's own graceful shutdown: Acquire failed: %v", err)
	}
	_ = lease.Release(context.Background())
}
