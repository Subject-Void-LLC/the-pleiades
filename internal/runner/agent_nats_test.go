package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/runner"
	"github.com/SubjectVoidLLC/the-pleiades/internal/topology"
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

func (a *alwaysFailAdapter) Execute(ctx context.Context, payload runner.DispatchPayload) error {
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
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsC.Terminate(ctx)

	url, err := natsC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	bus, err := event.NewNatsBus(ctx, url)
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
	agent := runner.NewAgent(consumer, adapter, js, topology.MaxDeliverDefault, slog.Default())

	agentCtx, cancelAgent := context.WithCancel(ctx)
	defer cancelAgent()
	go agent.Run(agentCtx)

	payload := runner.DispatchPayload{JobID: "dlq-job", RunbookID: "pb-1", DeviceName: "dev-1", DeviceIP: "10.0.0.1"}
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
	var dlqPayload runner.DispatchPayload
	if err := json.Unmarshal(dlqEvt.Data, &dlqPayload); err != nil {
		t.Fatalf("unmarshal dead-lettered payload: %v", err)
	}
	if dlqPayload.JobID != "dlq-job" {
		t.Errorf("dead-lettered JobID = %q, want dlq-job", dlqPayload.JobID)
	}
}
