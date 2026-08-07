package runner_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
)

// TestAgent_ExecuteWithLease_AcquiresAndReleasesRealLock proves
// handleMessage's own call into executeWithLease (agent_exec.go) both
// acquires and releases a real lock.Manager lease around Execute, against
// a real lock.NewInProcessManager(), not a mock: this is the relocated
// Phase 14 checklist item (PLAN.md Section 13's "a device can only have
// one exclusive execution running against it at a time") actually closed.
// Proof is a second Acquire for the same device succeeding immediately
// after the message is handled, not merely that Execute was called.
func TestAgent_ExecuteWithLease_AcquiresAndReleasesRealLock(t *testing.T) {
	locks := lock.NewInProcessManager()
	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-lease"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, locks, 5, slog.Default(), nil)

	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)

	lease, err := locks.Acquire(context.Background(), "device-lease", time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("device-lease was not released after handleMessage completed: Acquire failed: %v", err)
	}
	_ = lease.Release(context.Background())
}

// TestAgent_ExecuteWithLease_ContentionNaksInsteadOfDeadLettering proves
// that when a device's lease is already held by another execution,
// handleMessage Naks the message (to retry once the contention clears)
// rather than routing it through the Dead Letter Queue, which is reserved
// for a job that actually ran and failed. MockAdapter never errors, so if
// this test observed a DLQ path instead, it could only be because
// executeWithLease's own contention branch was not taken.
func TestAgent_ExecuteWithLease_ContentionNaksInsteadOfDeadLettering(t *testing.T) {
	locks := lock.NewInProcessManager()

	// Simulate a concurrent execution already holding the device.
	held, err := locks.Acquire(context.Background(), "device-contended", time.Minute, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("failed to pre-acquire device-contended: %v", err)
	}
	defer held.Release(context.Background())

	msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-contended"))}}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, locks, 5, slog.Default(), nil)

	runAgentUntil(t, agent, msg.naked.Load, 10*time.Second)

	if msg.ack.Load() {
		t.Error("a contended message must not be acked")
	}
	if msg.term.Load() {
		t.Error("a contended message must not be terminated (routed to DLQ); it should be retried, not given up on")
	}
}

// nakTrackingMsg extends MockMsg with a real, observable NakWithDelay
// (the base MockMsg's own NakWithDelay is a no-op stub), so
// TestAgent_ExecuteWithLease_ContentionNaksInsteadOfDeadLettering can
// prove the contention path actually calls it.
type nakTrackingMsg struct {
	*MockMsg
	naked atomic.Bool
}

func (m *nakTrackingMsg) NakWithDelay(delay time.Duration) error {
	m.naked.Store(true)
	return nil
}

// TestAgent_SelfAbort_InterruptibleCancelsExecution proves the heartbeat
// mechanism (agent_exec.go's heartbeat, driven by a short
// WithHeartbeatInterval so the test does not wait out the real production
// interval) self-aborts an in-flight, interruptible Execute call the
// moment a lease renewal fails, per PLAN.md Section 16's Network
// Partitions mitigation ("it self-aborts execution before the Controller
// TTL expires"). A fakeLease whose KeepAlive always fails is used
// deliberately here (not lock.NewInProcessManager()): forcing a real
// Manager's KeepAlive to fail deterministically, on demand, is not
// something its real implementation exposes a hook for, and this test is
// about Agent's own reaction to a lease failure, not the lease mechanism
// itself (already proven for real by the two tests above).
func TestAgent_SelfAbort_InterruptibleCancelsExecution(t *testing.T) {
	locks := &fakeLockManager{lease: &fakeLease{keepAliveErr: errors.New("deliberate keepalive failure")}}
	adapter := newSlowCancelAdapter()

	msg := &MockMsg{data: wireWrapDispatchPayload(interruptiblePayloadJSON("device-abort", true))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5, slog.Default(), nil,
		runner.WithHeartbeatInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.Run(ctx)

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}

	// The heartbeat ticks every 10ms and every KeepAlive fails, so
	// cancellation must arrive well within a couple of seconds if
	// self-abort is actually wired up.
	select {
	case <-adapter.cancelObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute never observed cancellation: self-abort did not fire for an interruptible task")
	}
	close(adapter.proceedAfterCancel)
}

// TestAgent_SelfAbort_NonInterruptibleRunsToCompletion proves the exact
// opposite of the test above: when payload.Interruptible is false,
// PLAN.md Section 16's own named exception applies ("Un-abortable tasks
// (interruptible: false) finish execution"), so a failing heartbeat must
// NOT cancel Execute. The adapter here completes on its own timer,
// independent of ctx, so a self-abort bug (canceling anyway) would show
// up as Execute observing ctx.Done() before it was ever supposed to.
func TestAgent_SelfAbort_NonInterruptibleRunsToCompletion(t *testing.T) {
	locks := &fakeLockManager{lease: &fakeLease{keepAliveErr: errors.New("deliberate keepalive failure")}}
	adapter := &selfPacedAdapter{done: make(chan struct{})}

	msg := &MockMsg{data: wireWrapDispatchPayload(interruptiblePayloadJSON("device-no-abort", false))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5, slog.Default(), nil,
		runner.WithHeartbeatInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.Run(ctx)

	// Let several heartbeat intervals elapse, all failing, while the
	// adapter is still deliberately running.
	time.Sleep(150 * time.Millisecond)
	if adapter.sawCancel.Load() {
		t.Fatal("Execute observed cancellation despite Interruptible=false: a failing heartbeat must not self-abort a non-interruptible task")
	}

	close(adapter.done) // let Execute finish on its own now
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !msg.ack.Load() {
		time.Sleep(2 * time.Millisecond)
	}
	if !msg.ack.Load() {
		t.Fatal("message was never acked: Execute did not run to its own natural completion")
	}
	if adapter.sawCancel.Load() {
		t.Fatal("Execute's context was canceled at some point during its run despite Interruptible=false")
	}
}

// TestAgent_SelfAbort_NonInterruptibleSurvivesOuterShutdown is the
// regression test for a real bug an adversarial review found: execCtx
// used to be a direct child of Agent.Run's own outer ctx
// (context.WithCancel(ctx)), so canceling that outer ctx -- exactly what
// a graceful shutdown (SIGTERM/SIGINT, cmd/runner/main.go) does -- aborted
// execution unconditionally, regardless of payload.Interruptible. PLAN.md
// Section 16's own named exception ("Un-abortable tasks... finish
// execution") was therefore only ever honored against a lease-heartbeat
// failure, never against the Runner process shutting down, which is a
// real and far more routine trigger (an ordinary redeploy or restart).
// This test cancels the outer Agent ctx directly, not a heartbeat, and
// proves a non-interruptible execution is unaffected by it.
func TestAgent_SelfAbort_NonInterruptibleSurvivesOuterShutdown(t *testing.T) {
	locks := lock.NewInProcessManager() // real lock: heartbeat's own KeepAlive never fails here
	adapter := newSelfPacedAdapter()

	msg := &MockMsg{data: wireWrapDispatchPayload(interruptiblePayloadJSON("device-shutdown-no-abort", false))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, locks, 5, slog.Default(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}

	// This is the outer Agent.Run shutdown signal itself, the exact thing
	// cmd/runner/main.go's own SIGTERM/SIGINT handler cancels -- not a
	// heartbeat failure.
	cancel()
	time.Sleep(150 * time.Millisecond)
	if adapter.sawCancel.Load() {
		t.Fatal("Execute observed cancellation from the outer Agent shutdown signal despite Interruptible=false")
	}

	close(adapter.done) // let Execute finish on its own now
	select {
	case err := <-runErr:
		if err != context.Canceled {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after the in-flight non-interruptible execution finished on its own")
	}

	if !msg.ack.Load() {
		t.Fatal("message was never acked: Execute did not run to its own natural completion despite Interruptible=false")
	}
	if adapter.sawCancel.Load() {
		t.Fatal("Execute's context was canceled at some point during its run despite Interruptible=false")
	}
}

// selfPacedAdapter finishes only when the test closes done, recording
// whether ctx was ever canceled while it ran (sawCancel), and signaling
// started the instant Execute is entered, for
// TestAgent_SelfAbort_NonInterruptibleRunsToCompletion and
// TestAgent_SelfAbort_NonInterruptibleSurvivesOuterShutdown.
type selfPacedAdapter struct {
	done      chan struct{}
	started   chan struct{}
	sawCancel atomic.Bool
}

func newSelfPacedAdapter() *selfPacedAdapter {
	return &selfPacedAdapter{done: make(chan struct{}), started: make(chan struct{})}
}

func (a *selfPacedAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	if a.started != nil {
		close(a.started)
	}
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		a.sawCancel.Store(true)
		<-a.done
		return nil
	}
}

// panicAdapter always panics, for
// TestAgent_ExecuteWithLease_RecoversAdapterPanic.
type panicAdapter struct{}

func (panicAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	panic("simulated adapter panic")
}

// TestAgent_ExecuteWithLease_RecoversAdapterPanic is the regression test
// for a real bug an adversarial review found and empirically reproduced:
// executeWithLease had no panic recovery anywhere between it and worker/
// handleMessage, so a panicking ExecutionAdapter.Execute (nothing today,
// but nothing preventing a future real adapter -- a failed type assertion
// against unexpected transport output, a nil dereference) crashed the
// entire Runner process, taking every other concurrently in-flight worker
// down with it before any of them could run their own deferred
// lease.Release. Proof here is that the test process survives at all
// (the historical bug would have crashed it), the panicking message is
// routed through the DLQ/Nak path rather than acked (a panic is treated
// as an ordinary execution failure, mirroring
// internal/event/consumer.go's own handleDelivery panic-recovery
// precedent), and the device lease is genuinely released despite the
// panic (a fresh Acquire for the same device succeeds immediately).
func TestAgent_ExecuteWithLease_RecoversAdapterPanic(t *testing.T) {
	locks := lock.NewInProcessManager()
	msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-panic"))}}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, panicAdapter{}, nil, locks, 5, slog.Default(), nil)

	runAgentUntil(t, agent, msg.naked.Load, 10*time.Second)

	if msg.ack.Load() {
		t.Error("a panicking execution must not be acked")
	}
	if msg.term.Load() {
		t.Error("a panicking execution must not be terminated outright; it should be retried like any other execution failure")
	}

	lease, err := locks.Acquire(context.Background(), "device-panic", time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("device-panic was not released after the panicking execution unwound: Acquire failed: %v", err)
	}
	_ = lease.Release(context.Background())
}

// TestNewAgent_WithLeaseTTL_OverridesDefault proves WithLeaseTTL actually
// changes the ttl executeWithLease presents to lock.Manager.Acquire,
// rather than the option existing but never being wired to anything.
func TestNewAgent_WithLeaseTTL_OverridesDefault(t *testing.T) {
	locks := &fakeLockManager{lease: &fakeLease{}}
	const overrideTTL = 42 * time.Second

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-ttl"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, locks, 5, slog.Default(), nil,
		runner.WithLeaseTTL(overrideTTL))

	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)

	if got := locks.lastAcquireTTL(); got != overrideTTL {
		t.Errorf("Acquire was called with ttl=%v, want the WithLeaseTTL override %v", got, overrideTTL)
	}
}

// fakeLockManager is a lock.Manager test double whose Acquire always
// succeeds and always returns the same lease. Used only by the self-abort
// tests above, which need a lease whose KeepAlive fails deterministically
// on demand -- a real lock.Manager (lock.NewInProcessManager(),
// lock.NewNatsLockManager) exposes no hook to force that, and every other
// test in this package uses a real one deliberately (see
// TestAgent_ExecuteWithLease_AcquiresAndReleasesRealLock's own doc
// comment).
type fakeLockManager struct {
	lease *fakeLease

	mu          sync.Mutex
	capturedID  string
	capturedTTL time.Duration
}

func (m *fakeLockManager) Acquire(ctx context.Context, itemID string, ttl time.Duration, opts lock.AcquireOptions) (lock.Lease, error) {
	m.mu.Lock()
	m.capturedID = itemID
	m.capturedTTL = ttl
	m.mu.Unlock()
	return m.lease, nil
}

func (m *fakeLockManager) lastAcquireTTL() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.capturedTTL
}

func (m *fakeLockManager) Close() error { return nil }

// fakeLease is a lock.Lease test double whose KeepAlive always returns
// keepAliveErr (if set) and whose Release always succeeds.
type fakeLease struct {
	keepAliveErr error
}

func (l *fakeLease) ID() string { return "fake-lease" }

func (l *fakeLease) KeepAlive(ctx context.Context) error {
	return l.keepAliveErr
}

func (l *fakeLease) Release(ctx context.Context) error { return nil }
