package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	natscontainer "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// alwaysFailAdapter is a runner.ExecutionAdapter that always errors, so
// TestAgent_FailedExecutionEventuallyDeadLetters can prove Agent's own DLQ
// integration (handleMessage's call to event.HandleDeliveryFailure)
// against a real broker, not just against event.HandleDeliveryFailure in
// isolation (already proven by internal/event's own container tests).
type alwaysFailAdapter struct {
	attempts chan struct{}
}

func (a *alwaysFailAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	a.attempts <- struct{}{}
	return errors.New("deliberate execution failure")
}

// TestAgent_FailedExecutionEventuallyDeadLetters proves runner.Agent's own
// Dead Letter Queue integration end to end, against a real NATS
// container: a dispatch whose adapter always fails is redelivered up to
// topology.MaxDeliverDefault times (visible as repeated Execute calls),
// then dead-lettered via the same event.HandleDeliveryFailure mechanism
// natsBus.Subscribe itself uses, per PLAN.md Section 25's Build-Once rule.
// This closes the one real gap runner_test.go's other tests leave: they
// all use an adapter that never fails, so none of them exercise this
// path.
func TestAgent_FailedExecutionEventuallyDeadLetters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	natsC, err := natscontainer.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsC.Terminate(ctx)

	url, err := natsC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	bus, err := event.NewNatsBus(ctx, url, nil)
	if err != nil {
		t.Fatalf("nats bus: %v", err)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}

	adapter := &alwaysFailAdapter{attempts: make(chan struct{}, topology.MaxDeliverDefault+2)}
	agent := runner.NewAgent(consumer, adapter, js, lock.NewInProcessManager(), topology.MaxDeliverDefault, slog.Default(), nil)

	agentCtx, cancelAgent := context.WithCancel(ctx)
	defer cancelAgent()
	go agent.Run(agentCtx)

	payload := wire.DispatchPayload{JobID: testJobID, RunbookID: "pb-1", DeviceID: "dlq-device", DeviceName: "dev-1", DeviceHost: "10.0.0.1"}
	evt, err := event.WrapPayload("dispatch-1", "runbook.dispatched", payload)
	if err != nil {
		t.Fatalf("wrap payload: %v", err)
	}
	if err := bus.Publish(ctx, topology.DispatchSubject(), *evt); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for i := 0; i < topology.MaxDeliverDefault; i++ {
		select {
		case <-adapter.attempts:
		case <-time.After(20 * time.Second):
			t.Fatalf("timed out waiting for execution attempt %d/%d", i+1, topology.MaxDeliverDefault)
		}
	}

	// No further attempts beyond MaxDeliverDefault.
	select {
	case <-adapter.attempts:
		t.Fatal("adapter was invoked beyond MaxDeliverDefault; the message was not terminated after exhaustion")
	case <-time.After(3 * time.Second):
	}

	dlqConsumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, jetstream.ConsumerConfig{
		FilterSubject: topology.DeadLetterSubject(topology.DispatchSubject()),
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	})
	if err != nil {
		t.Fatalf("dlq consumer: %v", err)
	}
	msgs, err := dlqConsumer.Fetch(1, jetstream.FetchMaxWait(10*time.Second))
	if err != nil {
		t.Fatalf("fetch dlq: %v", err)
	}
	var dlqMsg jetstream.Msg
	for m := range msgs.Messages() {
		dlqMsg = m
	}
	if dlqMsg == nil {
		t.Fatal("no message arrived on the dead letter subject after Agent exhausted redelivery")
	}

	var envelope event.DeadLetterEnvelope
	if err := json.Unmarshal(dlqMsg.Data(), &envelope); err != nil {
		t.Fatalf("unmarshal dead letter envelope: %v", err)
	}
	if envelope.OriginalSubject != topology.DispatchSubject() {
		t.Errorf("OriginalSubject = %q, want %q", envelope.OriginalSubject, topology.DispatchSubject())
	}

	var dlqEvt event.Event
	if err := json.Unmarshal(envelope.Payload, &dlqEvt); err != nil {
		t.Fatalf("unmarshal dead-lettered event: %v", err)
	}
	var dlqPayload wire.DispatchPayload
	if err := json.Unmarshal(dlqEvt.Data, &dlqPayload); err != nil {
		t.Fatalf("unmarshal dead-lettered payload: %v", err)
	}
	if dlqPayload.JobID != testJobID {
		t.Errorf("dead-lettered JobID = %q, want %q", dlqPayload.JobID, testJobID)
	}
}

// countingAdapter records every (JobID, DeviceID) pair Execute is called
// with, so a test can assert both a total count and, separately, that no
// key was ever seen more than once -- the actual "without duplicating
// execution" claim, which a bare count alone cannot prove (five
// executions could still mean one device executed five times and four
// not at all).
type countingAdapter struct {
	mu   sync.Mutex
	seen map[string]int
}

func newCountingAdapter() *countingAdapter {
	return &countingAdapter{seen: make(map[string]int)}
}

func (a *countingAdapter) Execute(ctx context.Context, payload wire.DispatchPayload) error {
	a.mu.Lock()
	a.seen[payload.JobID+":"+payload.DeviceID]++
	a.mu.Unlock()
	return nil
}

func (a *countingAdapter) total() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, c := range a.seen {
		n += c
	}
	return n
}

// duplicates returns every key Execute was called for more than once,
// with its own count, for a useful failure message.
func (a *countingAdapter) duplicates() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	dups := make(map[string]int)
	for k, c := range a.seen {
		if c > 1 {
			dups[k] = c
		}
	}
	return dups
}

// snapshot returns a copy of every key seen so far and its count, safe to
// read (e.g. in a t.Fatalf message) without racing concurrent Execute
// calls.
func (a *countingAdapter) snapshot() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]int, len(a.seen))
	for k, c := range a.seen {
		out[k] = c
	}
	return out
}

// TestAgent_ReleaseGate_PullsFiveDispatchesWithoutDuplicating is Phase
// 15's own literal Release Gate ("The Runner successfully pulls the 5
// events generated in Phase 14 off the bus without duplicating
// execution"), proven against a real NATS container with a real, bounded
// worker pool (WithPoolSize(3), agent_run.go) and a real
// lock.NewInProcessManager() (agent_exec.go): five real
// wire.DispatchPayload dispatch events, one shared JobID and five
// distinct DeviceIDs (mirroring a single Phase 14 fan-out), are published
// onto the real topology.DispatchSubject(); a pooled Agent must execute
// all five, and never execute the same (JobID, DeviceID) pair twice. The
// "without duplicating" half is what the per-device lock actually makes
// true: without it, two pool workers could legitimately race a
// redelivered or concurrently-pulled duplicate of the same device.
func TestAgent_ReleaseGate_PullsFiveDispatchesWithoutDuplicating(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	natsC, err := natscontainer.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsC.Terminate(ctx)

	url, err := natsC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	bus, err := event.NewNatsBus(ctx, url, nil)
	if err != nil {
		t.Fatalf("nats bus: %v", err)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.DispatchConsumerConfig())
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}

	adapter := newCountingAdapter()
	agent := runner.NewAgent(consumer, adapter, js, lock.NewInProcessManager(), topology.MaxDeliverDefault,
		slog.Default(), nil, runner.WithPoolSize(3))

	agentCtx, cancelAgent := context.WithCancel(ctx)
	defer cancelAgent()
	go agent.Run(agentCtx)

	jobID := uuid.New().String()
	for i := 0; i < 5; i++ {
		payload := wire.DispatchPayload{
			JobID:         jobID,
			RunbookID:     "pb-1",
			DeviceID:      fmt.Sprintf("release-gate-device-%d", i),
			DeviceName:    fmt.Sprintf("device-%d", i),
			DeviceHost:    "10.0.0.1",
			Interruptible: true,
		}
		evt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", payload)
		if err != nil {
			t.Fatalf("wrap payload %d: %v", i, err)
		}
		if err := bus.Publish(ctx, topology.DispatchSubject(), *evt); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) && adapter.total() < 5 {
		time.Sleep(10 * time.Millisecond)
	}

	if got := adapter.total(); got != 5 {
		t.Fatalf("total executions = %d, want 5 (seen: %v)", got, adapter.snapshot())
	}
	if dups := adapter.duplicates(); len(dups) != 0 {
		t.Fatalf("some (JobID, DeviceID) pairs executed more than once: %v", dups)
	}
}
