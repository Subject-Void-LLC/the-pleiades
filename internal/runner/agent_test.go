package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// testJobID is a syntactically valid UUID (uuid.Parse accepts it) used
// across this package's tests wherever a DispatchPayload needs a job_id
// that survives handleMessage's own uuid.Parse validation (agent.go's
// Schema/Injection Hardening check on the wire.DispatchPayload.JobID
// field, FAILURE_PATTERNS.md #84). Its value carries no other
// significance.
const testJobID = "11111111-1111-4111-8111-111111111111"

// dispatchPayloadJSON builds one wire.DispatchPayload's JSON body for a
// test fixture, with distinct deviceID and testJobID as the shared job
// this fixture's device belongs to -- mirroring
// wire.DispatchPayload.JobID's own real-world invariant ("every device
// targeted by a single DispatchRunbook call shares the same JobID").
// Giving every fixture device a distinct id, rather than every message in
// a batch sharing one empty deviceID, matters once executeWithLease
// (agent_exec.go) acquires a real per-device lock: two messages that
// share a deviceID now genuinely contend for the same lease, exactly the
// way two real dispatches against the same real device would, which a
// shared, empty deviceID would misrepresent as universal contention
// instead of the no-contention case a batch of distinct real devices
// actually is.
func dispatchPayloadJSON(deviceID string) string {
	// interruptible:true is explicit, not an omission this fixture leaves
	// to Go's own bool zero value: a real DispatchPayload on the wire is
	// never missing this key (internal/dispatch/worker_devices.go always
	// sets it from the resolved runbook.Runbook.Interruptible, which
	// itself defaults to true absent an explicit metadata.interruptible:
	// false in the source runbook), so a fixture that omitted it would
	// model a shape no real producer ever sends -- and would silently
	// pick the *opposite* default from a real interruptible runbook the
	// instant executeWithLease started treating Interruptible=false
	// differently from true (agent_exec.go's execCtx/detachedValueContext
	// split), which is exactly what happened here before this comment was
	// added: every test built on this fixture stopped observing outer
	// ctx cancellation, because they were all unknowingly exercising the
	// non-interruptible path.
	return fmt.Sprintf(`{"job_id":%q,"runbook_id":"pb-1","device_id":%q,"device_name":"router-1","device_host":"10.0.0.1","interruptible":true}`,
		testJobID, deviceID)
}

// interruptiblePayloadJSON is dispatchPayloadJSON plus an explicit
// interruptible value, for tests exercising executeWithLease's own
// self-abort-vs-run-to-completion branch (agent_exec.go), which reads
// wire.DispatchPayload.Interruptible directly.
func interruptiblePayloadJSON(deviceID string, interruptible bool) string {
	return fmt.Sprintf(`{"job_id":%q,"runbook_id":"pb-1","device_id":%q,"device_name":"router-1","device_host":"10.0.0.1","interruptible":%t}`,
		testJobID, deviceID, interruptible)
}

// wireWrapDispatchPayload builds the wire-format bytes handleMessage
// actually decodes: an Event envelope (internal/event) whose Data field
// holds the marshaled DispatchPayload JSON. Every real publish in this
// codebase now goes through Bus.Publish, which wraps a domain payload this
// same way -- a bare, unwrapped DispatchPayload is not what ever appears
// on the wire, so tests must not construct one either.
//
// Takes no testing handle (usable from *testing.F's top-level f.Add calls,
// which run before any *testing.T exists) and panics on a marshal
// failure, which a fixed json.RawMessage literal cannot realistically
// produce.
func wireWrapDispatchPayload(payloadJSON string) []byte {
	evt := event.Event{Data: json.RawMessage(payloadJSON)}
	data, err := json.Marshal(evt)
	if err != nil {
		panic(err)
	}
	return data
}

type MockConsumer struct {
	jetstream.Consumer
	PayloadMsgs []jetstream.Msg
	Calls       int
}

func (m *MockConsumer) FetchNoWait(batch int) (jetstream.MessageBatch, error) {
	m.Calls++
	if len(m.PayloadMsgs) == 0 {
		return nil, errors.New("no messages")
	}

	msgs := m.PayloadMsgs
	m.PayloadMsgs = nil // clear so next fetch gets error and we can see backoff

	mb := &MockMessageBatch{msgs: msgs}
	return mb, nil
}

type MockMessageBatch struct {
	msgs []jetstream.Msg
}

func (m *MockMessageBatch) Messages() <-chan jetstream.Msg {
	ch := make(chan jetstream.Msg, len(m.msgs))
	for _, msg := range m.msgs {
		ch <- msg
	}
	close(ch)
	return ch
}

func (m *MockMessageBatch) Error() error {
	return nil
}

type MockMsg struct {
	jetstream.Msg
	data []byte
	// ack and term are atomic.Bool, not plain bool: the bounded worker
	// pool (agent_run.go) means a real Ack/Term call now genuinely races
	// a concurrent test-goroutine read of the same MockMsg (e.g.
	// TestAgent_ReleaseGate's own completion poll, which must observe
	// progress while other workers are still executing, not only after
	// Run has fully returned). A plain bool here would be exactly the
	// kind of "safe in every test that happened not to read concurrently"
	// bug AGENTS.md's own -race requirement exists to catch.
	ack     atomic.Bool
	term    atomic.Bool
	ackErr  error
	termErr error
}

func (m *MockMsg) Data() []byte {
	return m.data
}

func (m *MockMsg) Ack() error {
	m.ack.Store(true)
	return m.ackErr
}

func (m *MockMsg) Term() error {
	m.term.Store(true)
	return m.termErr
}

// Metadata, Subject, and NakWithDelay back handleMessage's DLQ-routing
// branch (event.HandleDeliveryFailure), which needs enough of a real
// jetstream.Msg to compute a redelivery decision: NumDelivered fixed
// below any reasonable maxDeliver keeps that decision on the
// NakWithDelay path, so tests using these do not also need to fake
// jetstream.JetStream.Publish for the dead-letter-republish path (already
// proven against a real broker by agent_nats_test.go).
func (m *MockMsg) Metadata() (*jetstream.MsgMetadata, error) {
	return &jetstream.MsgMetadata{NumDelivered: 1}, nil
}

func (m *MockMsg) Subject() string { return "pleiades.jobs.dispatch" }

// Headers backs handleMessage's trace-context extraction. It returns nil,
// the shape of a message published with tracing disabled, which is the
// right default for every test here that is not about tracing; the one
// that is supplies its own (agent_trace_test.go's tracingMockMsg).
//
// It must exist rather than fall through to the embedded jetstream.Msg:
// that interface field is nil in this mock, so an unimplemented method
// called by production code is a nil dereference, not a compile error.
func (m *MockMsg) Headers() nats.Header { return nil }

func (m *MockMsg) NakWithDelay(delay time.Duration) error { return nil }

func TestAgent_ReleaseGate(t *testing.T) {
	// Release Gate: A mock Runner agent spins up, binds to a JetStream, pulls a batch of jobs, and logs acknowledgment.
	// For testing, we mock 10,000 jobs.
	var msgs []jetstream.Msg
	mockMsgs := make([]*MockMsg, 10000)

	for i := 0; i < 10000; i++ {
		mockMsgs[i] = &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON(fmt.Sprintf("device-%d", i)))}
		msgs = append(msgs, mockMsgs[i])
	}

	consumer := &MockConsumer{PayloadMsgs: msgs}
	logger := slog.Default()
	// js is nil: MockAdapter never returns an error, so handleMessage's
	// DLQ path (the only code that touches js) is never reached here.
	// lock.NewInProcessManager() is a real Manager, not a mock: every one
	// of these 10,000 devices has its own distinct DeviceID
	// (dispatchPayloadJSON), so the bounded worker pool (agent_run.go)
	// can genuinely drain them concurrently with no real contention,
	// proving "without duplicating execution" against real lock
	// acquire/release, not just against a count.
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- agent.Run(ctx) }()

	// Poll for completion rather than sleeping a fixed guess: the pooled,
	// lock-acquiring path (agent_exec.go) does real per-message work now
	// (a goroutine, a lock acquire/release, a ticker), so a duration
	// tuned for the old serial, lock-free loop would be an arbitrary
	// guess at throughput, not a real bound the Release Gate's own text
	// ("pulls the 5 events... without duplicating execution") requires.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if countAcked(mockMsgs) == 10000 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()

	if err := <-runErr; err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	if ackCount := countAcked(mockMsgs); ackCount != 10000 {
		t.Errorf("expected 10000 messages to be acked, got %d", ackCount)
	}
}

// countAcked counts how many of msgs have been Ack'd. Shared by
// TestAgent_ReleaseGate's completion poll and its final assertion, so the
// two cannot silently drift apart on what "acked" means.
func countAcked(msgs []*MockMsg) int {
	count := 0
	for _, m := range msgs {
		if m.ack.Load() {
			count++
		}
	}
	return count
}

// MockAdapter is the shared runner.ExecutionAdapter stub for this package's
// tests and benchmarks. It used to be declared a second, identical time in
// agent_bench_test.go, which is the actual go vet failure this session found
// pre-existing (see FAILURE_PATTERNS.md #14); that duplicate is removed, not
// deferred, since the fix is mechanical and does not touch Phase 15's real
// scope (the Runner Agent Scaffold, .SPECIFICATION/IMPLEMENTATION.md), which
// still owns building a real adapter, not this no-op stand-in.
type MockAdapter struct{}

func (m *MockAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	return wire.Outcome{}, nil
}

// erroringAdapter is a runner.ExecutionAdapter that always fails, so
// tests can exercise handleMessage's own DLQ-routing branch
// (event.HandleDeliveryFailure) without needing a real broker for every
// scenario; TestAgent_FailedExecutionEventuallyDeadLetters
// (agent_nats_test.go) is the real-broker proof this unit-level test
// complements, not replaces.
type erroringAdapter struct{}

func (erroringAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	return wire.Outcome{}, errors.New("deliberate execution failure")
}

func TestNewAgent_DefaultsNilLoggerToSlogDefault(t *testing.T) {
	consumer := &MockConsumer{}
	// Must not panic on a nil logger; NewAgent substitutes slog.Default().
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := agent.Run(ctx); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAgent_HandleMessage_ToleratesAckFailure(t *testing.T) {
	msg := &MockMsg{
		data:   wireWrapDispatchPayload(dispatchPayloadJSON("device-1")),
		ackErr: errors.New("deliberate ack failure"),
	}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil)

	// Must not panic even though Ack itself fails.
	runAgentUntil(t, agent, msg.ack.Load, 10*time.Second)
}

func TestAgent_HandleMessage_ToleratesTermFailureOnMalformedMessage(t *testing.T) {
	msg := &MockMsg{
		data:    []byte("this is not a valid envelope"),
		termErr: errors.New("deliberate term failure"),
	}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil)

	// Must not panic even though Term itself fails.
	runAgentUntil(t, agent, msg.term.Load, 10*time.Second)
}

func TestAgent_HandleMessage_AdapterFailureRoutesThroughDeadLetterHandling(t *testing.T) {
	msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-1"))}}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	// js is nil: msg.Metadata() reports NumDelivered=1 against maxDeliver=5,
	// so HandleDeliveryFailure takes the NakWithDelay branch (also
	// overridden on MockMsg), never the dead-letter-republish branch that
	// would need a working js.Publish.
	agent := runner.NewAgent(consumer, erroringAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil)

	runAgentUntil(t, agent, msg.naked.Load, 10*time.Second)

	if msg.ack.Load() {
		t.Error("expected the message not to be acked after an adapter failure")
	}
}

func TestAgent_HandleMessage_MalformedDispatchPayloadInsideValidEnvelope(t *testing.T) {
	// A syntactically valid Event envelope whose Data is not a valid
	// DispatchPayload: handleMessage's second unmarshal step (evt.Data ->
	// DispatchPayload) is what should reject this, not the first (raw
	// bytes -> Event).
	msg := &MockMsg{data: []byte(`{"id":"e1","data":"not an object"}`)}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, &MockAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil)

	runAgentUntil(t, agent, msg.term.Load, 10*time.Second)
}

// unroutableAdapter stands in for a Router that has no adapter for the
// dispatched kind. It returns the Router's own sentinel, which is what the
// Agent branches on.
type unroutableAdapter struct{}

func (unroutableAdapter) Execute(context.Context, wire.DispatchPayload) (wire.Outcome, error) {
	return wire.Outcome{}, fmt.Errorf("%w: %q", routing.ErrNoAdapter, "playbook")
}

// TestAgent_HandleMessage_AnUnroutableKindIsReportedThenTerminated covers
// the one failure that would otherwise be invisible everywhere.
//
// Term produces no bus traffic at all, and the Controller has already
// written a JobTask row saying this device was dispatched. Without the
// report, a kind this fleet cannot run would leave the job saying
// "dispatched" forever, and the only symptom an operator would ever see is
// a run that never finishes.
//
// Terminated rather than Nak'd, because redelivery cannot help: this binary
// will not grow an adapter between two deliveries of the same message, so a
// Nak retries to maxDeliver and then dead-letters something that never ran.
func TestAgent_HandleMessage_AnUnroutableKindIsReportedThenTerminated(t *testing.T) {
	msg := &nakTrackingMsg{MockMsg: &MockMsg{data: wireWrapDispatchPayload(dispatchPayloadJSON("device-1"))}}
	consumer := &MockConsumer{PayloadMsgs: []jetstream.Msg{msg}}
	agent := runner.NewAgent(consumer, unroutableAdapter{}, nil, lock.NewInProcessManager(), 5, slog.Default(), nil)

	runAgentUntil(t, agent, msg.term.Load, 10*time.Second)

	if msg.naked.Load() {
		t.Error("an unroutable kind was Nak'd for redelivery, which can never make it routable")
	}
	if msg.ack.Load() {
		t.Error("an unroutable kind was acked as if it had run")
	}
}
