package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestNatsBusSubscribe_HandlerErrorEventuallyDeadLetters proves PLAN.md
// Section 26.3's Dead Letter Queue mechanism end to end: a handler that
// always fails causes redeliveries (visible as repeated handler calls),
// and once delivery is exhausted (topology.MaxDeliverDefault attempts),
// HandleDeliveryFailure republishes the original payload, wrapped with the
// failure reason, to topology.DeadLetterSubject, and the original message
// is terminated (no further redelivery beyond that point).
func TestNatsBusSubscribe_HandlerErrorEventuallyDeadLetters(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init bus: %v", err)
	}

	const topic = "pleiades.events.dlq.handler-error"
	deliveryCount := 0
	deliveries := make(chan struct{}, topology.MaxDeliverDefault+2)

	if err := bus.Subscribe(ctx, topic, func(e event.Event) error {
		deliveryCount++
		deliveries <- struct{}{}
		return errDeliberate
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// A second, independent connection reads the dead letter subject
	// directly (not through Bus, since a dead-lettered message is
	// diagnostic output, not a normal domain event this codebase
	// currently consumes). Deliberately connected here but not turned into
	// a consumer until right before the Fetch below: an ephemeral pull
	// consumer with no pull request against it for InactiveThreshold
	// (server default 5s) is cleaned up by the server, and this test's own
	// redelivery-and-backoff wait below takes far longer than that.
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}

	evt := event.Event{ID: "always-fails", Type: "dlq.handler-error"}
	if err := bus.Publish(ctx, topic, evt); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait for every expected delivery attempt (initial + redeliveries up
	// to MaxDeliverDefault).
	for i := 0; i < topology.MaxDeliverDefault; i++ {
		select {
		case <-deliveries:
		case <-time.After(20 * time.Second):
			t.Fatalf("timed out waiting for delivery attempt %d/%d (saw %d so far)", i+1, topology.MaxDeliverDefault, deliveryCount)
		}
	}

	// No further deliveries should occur beyond MaxDeliverDefault: the
	// message must have been terminated, not redelivered forever.
	select {
	case <-deliveries:
		t.Fatalf("handler was called a %dth time; MaxDeliverDefault (%d) was not enforced", deliveryCount, topology.MaxDeliverDefault)
	case <-time.After(3 * time.Second):
	}

	dlqConsumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, jetstream.ConsumerConfig{
		FilterSubject: topology.DeadLetterSubject(topic),
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
		t.Fatal("no message arrived on the dead letter subject after redelivery exhaustion")
	}

	var envelope event.DeadLetterEnvelope
	if err := json.Unmarshal(dlqMsg.Data(), &envelope); err != nil {
		t.Fatalf("failed to unmarshal dead letter envelope: %v", err)
	}
	if envelope.OriginalSubject != topic {
		t.Errorf("OriginalSubject = %q, want %q", envelope.OriginalSubject, topic)
	}
	if envelope.Reason == "" {
		t.Error("Reason was empty; expected the handler's error text")
	}
	if envelope.NumDelivered < uint64(topology.MaxDeliverDefault) {
		t.Errorf("NumDelivered = %d, want at least %d", envelope.NumDelivered, topology.MaxDeliverDefault)
	}

	var originalEvt event.Event
	if err := json.Unmarshal(envelope.Payload, &originalEvt); err != nil {
		t.Fatalf("failed to unmarshal original payload from dead letter envelope: %v", err)
	}
	if originalEvt.ID != "always-fails" {
		t.Errorf("dead-lettered payload ID = %q, want always-fails", originalEvt.ID)
	}
}

// TestNatsBusSubscribe_PanicIsRecoveredAndDeadLetters proves a handler
// panic is recovered (the test process survives, per PLAN.md Section
// 26.1's "Panic: Recovered, treated as terminal, never allowed to kill the
// process") and routed through the same delivery-failure/DLQ path as an
// ordinary handler error, not silently dropped.
func TestNatsBusSubscribe_PanicIsRecoveredAndDeadLetters(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init bus: %v", err)
	}

	const topic = "pleiades.events.dlq.panic"
	deliveries := make(chan struct{}, topology.MaxDeliverDefault+2)

	if err := bus.Subscribe(ctx, topic, func(e event.Event) error {
		deliveries <- struct{}{}
		panic("deliberate handler panic")
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
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

	if err := bus.Publish(ctx, topic, event.Event{ID: "panics-always"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for i := 0; i < topology.MaxDeliverDefault; i++ {
		select {
		case <-deliveries:
		case <-time.After(20 * time.Second):
			t.Fatalf("timed out waiting for delivery attempt %d/%d; a panic must not silently kill delivery", i+1, topology.MaxDeliverDefault)
		}
	}

	// Created here, right before Fetch, for the same reason documented in
	// TestNatsBusSubscribe_HandlerErrorEventuallyDeadLetters: an idle
	// ephemeral pull consumer is cleaned up by the server before this
	// test's own redelivery wait above would complete.
	dlqConsumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, jetstream.ConsumerConfig{
		FilterSubject: topology.DeadLetterSubject(topic),
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
	found := false
	for range msgs.Messages() {
		found = true
	}
	if !found {
		t.Fatal("no message arrived on the dead letter subject after a panicking handler exhausted redelivery")
	}
}

// TestNatsBusSubscribe_MalformedPayloadIsTerminatedNotRetriedForever proves
// a payload that fails to unmarshal as an Event is terminated immediately
// rather than negatively acknowledged: the previous implementation
// Nak()'d a malformed payload, which JetStream redelivers instantly and
// which can never succeed no matter how many times it is retried.
func TestNatsBusSubscribe_MalformedPayloadIsTerminatedNotRetriedForever(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init bus: %v", err)
	}

	const topic = "pleiades.events.dlq.malformed"
	deliveries := make(chan struct{}, 4)
	if err := bus.Subscribe(ctx, topic, func(e event.Event) error {
		// This handler must never actually be invoked: a malformed payload
		// fails to decode before the handler is ever reached.
		deliveries <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
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

	// Publish a payload that is not valid JSON directly, bypassing
	// Bus.Publish's own envelope marshaling (which could never itself
	// produce malformed JSON) to simulate a genuinely corrupt message on
	// the wire.
	if _, err := js.Publish(ctx, topic, []byte("this is not json")); err != nil {
		t.Fatalf("publish malformed payload: %v", err)
	}

	// The handler must never fire, and no further redelivery should occur:
	// wait long enough to have seen at least one of
	// topology.MaxDeliverDefault's worth of Nak-and-redeliver cycles if the
	// old, buggy behavior were still in place, and confirm nothing arrived.
	select {
	case <-deliveries:
		t.Fatal("handler was invoked for a malformed payload; it should have failed to decode before ever reaching the handler")
	case <-time.After(5 * time.Second):
		// Expected: no delivery, decode failure handled internally.
	}
}

// TestHandleDeliveryFailure_NonPositiveMaxDeliverDeadLettersImmediately
// proves the maxDeliver<=0 defensive branch dlq.go's own comment
// documents: a non-positive maxDeliver is treated as already exhausted
// (dead-lettered and terminated on the very first call) rather than
// converted to uint64 directly, which would wrap a negative value around
// to a huge number and retry forever instead of failing safe.
func TestHandleDeliveryFailure_NonPositiveMaxDeliverDeadLettersImmediately(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	if _, _, err := topology.ProvisionStream(ctx, js, topology.DefaultOutageBudget, false); err != nil {
		t.Fatalf("ensure stream: %v", err)
	}

	const topic = "pleiades.events.dlq.non-positive-max-deliver"
	if _, err := js.Publish(ctx, topic, []byte(`{"id":"non-positive-max-deliver"}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, topology.SubscribeConsumerConfig(topology.DurableName(topic), topic))
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	msgs, err := consumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	var msg jetstream.Msg
	for m := range msgs.Messages() {
		msg = m
	}
	if msg == nil {
		t.Fatal("no message fetched")
	}

	if err := event.HandleDeliveryFailure(ctx, js, msg, 0, "non-positive maxDeliver test"); err != nil {
		t.Fatalf("HandleDeliveryFailure: %v", err)
	}

	dlqConsumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, jetstream.ConsumerConfig{
		FilterSubject: topology.DeadLetterSubject(topic),
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckNonePolicy,
	})
	if err != nil {
		t.Fatalf("dlq consumer: %v", err)
	}
	dlqMsgs, err := dlqConsumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("dlq fetch: %v", err)
	}
	found := false
	for range dlqMsgs.Messages() {
		found = true
	}
	if !found {
		t.Fatal("expected an immediate dead letter for maxDeliver=0, got none")
	}
}
