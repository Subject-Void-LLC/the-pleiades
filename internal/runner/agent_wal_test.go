package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
)

// idleConsumer returns msgs exactly once, then an empty, no-error batch
// on every subsequent call, matching a real jetstream.Consumer.
// FetchNoWait's own idle contract (nil error, zero messages). Unlike
// MockConsumer (agent_test.go), which returns an error once exhausted and
// therefore never exercises fetchLoop's own received==0 idle branch
// (agent_run.go), this is what actually drives flushWAL's own idle-tick
// retry (agent_wal.go).
type idleConsumer struct {
	jetstream.Consumer
	sent bool
	msgs []jetstream.Msg
}

func (c *idleConsumer) FetchNoWait(batch int) (jetstream.MessageBatch, error) {
	if !c.sent {
		c.sent = true
		return &MockMessageBatch{msgs: c.msgs}, nil
	}
	return &MockMessageBatch{msgs: nil}, nil
}

// flakyBus wraps a real event.Bus, failing exactly the first n Publish
// calls (n set via failCount) before delegating to inner. Used to
// simulate a real, temporary bus outage without needing a real broker to
// actually take down.
type flakyBus struct {
	inner     event.Bus
	failCount atomic.Int32
}

func (b *flakyBus) Publish(ctx context.Context, topic string, evt event.Event) error {
	if b.failCount.Add(-1) >= 0 {
		return errors.New("deliberate publish failure")
	}
	return b.inner.Publish(ctx, topic, evt)
}

func (b *flakyBus) Subscribe(ctx context.Context, topic string, handler func(event.Event) error) error {
	return b.inner.Subscribe(ctx, topic, handler)
}

func (b *flakyBus) Close() error { return b.inner.Close() }

// subscribeResultEntries subscribes to topology.ResultSubject(jobID) on
// bus and returns a channel every decoded runner.ResultEntry delivered
// there is sent on, for a test to assert against.
func subscribeResultEntries(t *testing.T, bus event.Bus, jobID string) <-chan runner.ResultEntry {
	t.Helper()
	received := make(chan runner.ResultEntry, 16)
	err := bus.Subscribe(context.Background(), topology.ResultSubject(jobID), func(evt event.Event) error {
		var entry runner.ResultEntry
		if err := json.Unmarshal(evt.Data, &entry); err != nil {
			t.Errorf("failed to decode result entry: %v", err)
			return nil
		}
		received <- entry
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	return received
}

// runAgentUntil runs agent.Run(ctx) in the background, waits for done to
// report true (polling, not a fixed sleep), then cancels ctx and waits
// for Run to return. A fixed sleep-then-cancel race, used by an earlier
// version of this file's tests, could cancel ctx while handleMessage was
// still mid-flight through real, fsync'd WAL file I/O under load,
// canceling the very Publish call a test wanted to observe succeed
// (event.Bus.Publish checks ctx.Err() up front, per its own doc comment)
// -- a real, reproducible flake, not a hypothetical one. Waiting for a
// concrete completion signal this package's own handleMessage already
// gives (Ack, or a tracked Nak) removes the race entirely, independent of
// how loaded the machine is.
func runAgentUntil(t *testing.T, agent *runner.Agent, done func() bool, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && !done() {
		time.Sleep(2 * time.Millisecond)
	}
	if !done() {
		t.Fatal("timed out waiting for the message to be handled")
	}

	cancel()
	select {
	case err := <-runErr:
		if err != context.Canceled {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(timeout):
		t.Fatal("Run() did not return after cancel")
	}
}

// TestAgent_ReportResult_SuccessfulExecutionFlushesToWAL proves the
// happy path end to end: a successful Execute is durably recorded in a
// real fileWAL (t.TempDir(), not a mock) and delivered over a real
// event.NewInProcessBus() to topology.ResultSubject(jobID), and the entry
// is acknowledged locally (no longer Pending) once delivery succeeds.
func TestAgent_ReportResult_SuccessfulExecutionFlushesToWAL(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	bus := event.NewInProcessBus()
	defer bus.Close()
	received := subscribeResultEntries(t, bus, testJobID)

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-wal-ok"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithResultWAL(wal, bus))

	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)

	// event.Bus.Publish (both adapters, including the in-process one)
	// delivers to subscribers asynchronously on their own goroutine and
	// does not wait for them (see inProcessBus.Publish's own doc
	// comment), so the entry can genuinely still be in flight for a
	// moment after Run itself has already returned; a bounded wait, not
	// an instant non-blocking check, is what a real subscriber's own
	// disposition looks like.
	select {
	case entry := <-received:
		if entry.Outcome != "completed" {
			t.Errorf("entry.Outcome = %q, want %q", entry.Outcome, "completed")
		}
		if entry.DeviceID != "device-wal-ok" {
			t.Errorf("entry.DeviceID = %q, want %q", entry.DeviceID, "device-wal-ok")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result entry was published")
	}

	pending, err := wal.Pending(context.Background())
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() = %+v, want empty (the entry should have been acknowledged after a successful flush)", pending)
	}
}

// TestAgent_ReportResult_UsesStableIdempotencyKeyAcrossRedelivery is the
// regression test for a real bug an adversarial review found and
// empirically reproduced: reportResult used to leave ResultEntry.ID
// blank, so fileWAL.Append minted a fresh random UUID on every call. A
// JetStream redelivery of the identical dispatch (a crash between this
// Runner's own local wal.Acknowledge succeeding and the broker-side
// msg.Ack landing) re-enters handleMessage from scratch and calls
// reportResult again, which used to mint a second, unrelated random ID --
// so the redelivered, re-executed job published as an entirely distinct
// job.result event that no idempotency-key dedup (event.Bus's own
// IdempotencyKey mechanism) could ever collapse back down to the original,
// producing two publishes for one logical outcome. This proves the fix
// (ID = payload.JobID+":"+payload.DeviceID, agent_wal.go) is deterministic
// and stays identical across what two separate handleMessage calls for
// the identical wire payload -- standing in for two delivery attempts of
// the same message -- each independently compute.
func TestAgent_ReportResult_UsesStableIdempotencyKeyAcrossRedelivery(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	bus := event.NewInProcessBus()
	defer bus.Close()
	received := subscribeResultEntries(t, bus, testJobID)

	payloadJSON := dispatchPayloadJSON("device-redelivered")
	for i := 0; i < 2; i++ {
		msg := &MockMsg{data: wireWrapDispatchPayload(payloadJSON)}
		consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
		agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
			runner.WithResultWAL(wal, bus))
		runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)
	}

	var ids []string
	for i := 0; i < 2; i++ {
		select {
		case entry := <-received:
			ids = append(ids, entry.ID)
		case <-time.After(5 * time.Second):
			t.Fatalf("only received %d of 2 expected result entries (ids so far: %v)", len(ids), ids)
		}
	}

	wantID := testJobID + ":device-redelivered"
	if ids[0] != wantID || ids[1] != wantID {
		t.Fatalf("entry IDs across two deliveries of the identical payload = %v, want both to equal the stable key %q", ids, wantID)
	}
}

// TestAgent_ReportResult_FailedExecutionRecordsFailedOutcome proves a
// genuine (non-contention) execution failure is also recorded, with
// Outcome "failed" and Reason carrying the error, alongside the existing
// Dead Letter Queue handling that failure also triggers -- the WAL and
// the DLQ are not mutually exclusive, they answer different questions
// ("what happened" durably, versus "should this redeliver").
func TestAgent_ReportResult_FailedExecutionRecordsFailedOutcome(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	bus := event.NewInProcessBus()
	defer bus.Close()
	received := subscribeResultEntries(t, bus, testJobID)

	msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-wal-fail"))}}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, erroringAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithResultWAL(wal, bus))

	// A genuine (non-contention) execution failure is Nak'd (retried via
	// the Dead Letter Queue path), not Acked, so completion is polled via
	// the Nak signal, not msg.ack.
	runAgentUntil(t, agent, msg.naked.Load, 10*time.Second)

	select {
	case entry := <-received:
		if entry.Outcome != "failed" {
			t.Errorf("entry.Outcome = %q, want %q", entry.Outcome, "failed")
		}
		if entry.Reason == "" {
			t.Error("entry.Reason is empty, want the adapter's own error message")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result entry was published for the failed execution")
	}
}

// TestAgent_ReportResult_ContentionIsNeverRecorded proves lock contention
// (executeWithLease returning errLockContention, agent_exec.go) does NOT
// produce a WAL entry: Execute was never actually invoked, so there is no
// real outcome to record, and doing so anyway would misreport "failed"
// for work that never ran.
func TestAgent_ReportResult_ContentionIsNeverRecorded(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	bus := event.NewInProcessBus()
	defer bus.Close()
	received := subscribeResultEntries(t, bus, testJobID)

	locks := lock.NewInProcessManager()
	held, err := locks.Acquire(context.Background(), "device-wal-contended", time.Minute, lock.AcquireOptions{})
	if err != nil {
		t.Fatalf("failed to pre-acquire device-wal-contended: %v", err)
	}
	defer held.Release(context.Background())

	msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-wal-contended"))}}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, locks, 5, slog.Default(), nil,
		runner.WithResultWAL(wal, bus))

	runAgentUntil(t, agent, msg.naked.Load, 10*time.Second)

	// Proving absence needs a real wait, not an instant check: Publish
	// dispatches to subscribers asynchronously (see the identical note
	// above), so an instant non-blocking check would trivially "pass" even
	// against a buggy implementation that does publish, simply because
	// its delivery goroutine had not run yet.
	select {
	case entry := <-received:
		t.Fatalf("a contended message must not produce a WAL/result entry, got %+v", entry)
	case <-time.After(200 * time.Millisecond):
	}
}

// failingAppendWAL wraps a real ResultWAL, forcing every Append call to
// fail, so TestAgent_ReportResult_LogsAppendFailureWithoutPanicking can
// exercise reportResult's own error-handling branch (agent_wal.go): a
// real WAL's Append essentially never fails in this test suite's own
// scenarios (a temp dir is always writable), so this is the one place a
// wrapping double, not the real fileWAL, is the right tool.
type failingAppendWAL struct {
	runner.ResultWAL
}

func (w *failingAppendWAL) Append(ctx context.Context, entry runner.ResultEntry) (runner.ResultEntry, error) {
	return runner.ResultEntry{}, errors.New("deliberate append failure")
}

// TestAgent_ReportResult_LogsAppendFailureWithoutPanicking proves a WAL
// Append failure is handled (logged) rather than panicking handleMessage
// or blocking the message's own Ack: the execution itself still
// succeeded, and a WAL append failure must not turn that into a stuck or
// crashed Runner.
func TestAgent_ReportResult_LogsAppendFailureWithoutPanicking(t *testing.T) {
	realWAL, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer realWAL.Close()
	wal := &failingAppendWAL{ResultWAL: realWAL}

	bus := event.NewInProcessBus()
	defer bus.Close()

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-wal-append-fail"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithResultWAL(wal, bus))

	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)
}

// failingAcknowledgeWAL wraps a real ResultWAL, forcing every
// Acknowledge call to fail, so
// TestAgent_FlushOne_LogsAcknowledgeFailureAfterSuccessfulPublish can
// exercise flushOne's own "flushed but not locally acknowledged" branch
// (agent_wal.go): the entry is still delivered (Publish succeeds against
// the real bus), only the local bookkeeping fails.
type failingAcknowledgeWAL struct {
	runner.ResultWAL
}

func (w *failingAcknowledgeWAL) Acknowledge(ctx context.Context, id string) error {
	return errors.New("deliberate acknowledge failure")
}

func TestAgent_FlushOne_LogsAcknowledgeFailureAfterSuccessfulPublish(t *testing.T) {
	realWAL, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer realWAL.Close()
	wal := &failingAcknowledgeWAL{ResultWAL: realWAL}

	bus := event.NewInProcessBus()
	defer bus.Close()
	received := subscribeResultEntries(t, bus, testJobID)

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-wal-ack-fail"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithResultWAL(wal, bus))

	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("entry was never delivered, even though the underlying bus Publish should have succeeded")
	}
}

// failingPendingWAL wraps a real ResultWAL, forcing every Pending call to
// fail, so TestAgent_FlushWAL_LogsPendingFailureWithoutPanicking can
// exercise flushWAL's own error-handling branch (agent_wal.go): a real
// WAL's Pending essentially never fails in this test suite's own
// scenarios, so this is the one place a wrapping double is the right
// tool.
type failingPendingWAL struct {
	runner.ResultWAL
}

func (w *failingPendingWAL) Pending(ctx context.Context) ([]runner.ResultEntry, error) {
	return nil, errors.New("deliberate pending failure")
}

// TestAgent_FlushWAL_LogsPendingFailureWithoutPanicking proves a WAL
// Pending failure, encountered on fetchLoop's own idle-tick retry
// (flushWAL), is handled (logged) rather than panicking the whole Agent
// or getting the fetch loop stuck.
func TestAgent_FlushWAL_LogsPendingFailureWithoutPanicking(t *testing.T) {
	realWAL, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer realWAL.Close()
	wal := &failingPendingWAL{ResultWAL: realWAL}

	bus := event.NewInProcessBus()
	defer bus.Close()

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-wal-pending-fail"))}
	consumer := &idleConsumer{msgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5,
		slog.New(slog.NewTextHandler(new(discardWriter), nil)), nil,
		runner.WithResultWAL(wal, bus))

	// runAgentUntil polls msg.ack: even though this message's own
	// eager flush never touches Pending (only flushWAL's idle-tick retry
	// does), a stuck or panicking fetch loop would also fail to have
	// acked it in the first place, so this is still a meaningful proof
	// that the Pending failure did not take the whole loop down.
	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)
}

// TestAgent_ReportResult_SurvivesShutdownDuringExecution is the
// regression test for a real bug an adversarial review found before this
// phase shipped: reportResult used to call wal.Append with the exact ctx
// Agent.Run's own graceful shutdown (or a self-abort) cancels, and
// fileWAL.Append's own ctx.Err() guard (wal_file.go) rejected the write
// outright whenever that ctx was already canceled -- silently losing the
// outcome of a job that had genuinely just finished (with a real error:
// context.Canceled) for precisely the scenario the WAL exists to protect
// against. slowCancelAdapter (agent_run_test.go) makes this reproducible
// deterministically: Execute observes cancellation, at which point the
// real code under test is exercised with ctx already canceled by the time
// reportResult runs.
func TestAgent_ReportResult_SurvivesShutdownDuringExecution(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	bus := event.NewInProcessBus()
	defer bus.Close()
	received := subscribeResultEntries(t, bus, testJobID)

	adapter := newSlowCancelAdapter()
	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-shutdown-wal"))}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, adapter, nil, lock.NewInProcessManager(), 5, slog.Default(), nil,
		runner.WithResultWAL(wal, bus))

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	select {
	case <-adapter.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute was never entered")
	}

	// Simulates both real triggers for this scenario at once: this is
	// exactly what Agent.Run's own graceful shutdown does (cancel the
	// outer ctx while a worker is mid-handleMessage), and structurally
	// identical to what a heartbeat-driven self-abort does to execCtx.
	cancel()

	select {
	case <-adapter.cancelObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute never observed cancellation")
	}
	close(adapter.proceedAfterCancel) // let Execute return ctx.Err() now

	select {
	case err := <-runErr:
		if err != context.Canceled {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return")
	}

	// The bug: this entry would never have been appended at all (fileWAL.
	// Append's own ctx.Err() guard rejects a write against an
	// already-canceled context), so it would never appear here, and would
	// never have been retried by any later flushWAL tick either, because
	// it was never durably recorded in the first place.
	select {
	case entry := <-received:
		if entry.Outcome != "failed" {
			t.Errorf("entry.Outcome = %q, want %q", entry.Outcome, "failed")
		}
		if entry.Reason == "" {
			t.Error("entry.Reason is empty, want context.Canceled's own message")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result entry was ever recorded for a job canceled by shutdown mid-execution: the outcome was silently lost")
	}
}

// TestAgent_FlushWAL_RetriesOnNextIdleTick proves the actual property the
// WAL exists for (PLAN.md Section 16's State Desync Mitigation): a
// publish that fails when the job finishes leaves the entry Pending
// rather than losing it, and a later idle-tick flush (fetchLoop's own
// received==0 branch, agent_run.go) delivers it once the bus recovers.
// idleConsumer, not MockConsumer, is required here specifically because
// only idleConsumer's nil-error-empty-batch shape actually drives that
// branch (see idleConsumer's own doc comment).
func TestAgent_FlushWAL_RetriesOnNextIdleTick(t *testing.T) {
	wal, err := runner.NewFileWAL(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWAL: %v", err)
	}
	defer wal.Close()

	realBus := event.NewInProcessBus()
	defer realBus.Close()
	received := subscribeResultEntries(t, realBus, testJobID)

	bus := &flakyBus{inner: realBus}
	bus.failCount.Store(1) // the eager flush inside reportResult fails exactly once

	msg := &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-wal-retry"))}
	consumer := &idleConsumer{msgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5,
		slog.New(slog.NewTextHandler(new(discardWriter), nil)), nil,
		runner.WithResultWAL(wal, bus))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go agent.Run(ctx)

	select {
	case entry := <-received:
		if entry.DeviceID != "device-wal-retry" {
			t.Fatalf("entry.DeviceID = %q, want %q", entry.DeviceID, "device-wal-retry")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("result entry was never delivered after the simulated bus outage cleared")
	}

	deadline := time.Now().Add(5 * time.Second)
	var pending []runner.ResultEntry
	for time.Now().Before(deadline) {
		pending, err = wal.Pending(context.Background())
		if err != nil {
			t.Fatalf("Pending: %v", err)
		}
		if len(pending) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() = %+v, want empty once the retried flush's own Acknowledge has run", pending)
	}
}
