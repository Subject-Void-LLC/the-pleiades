package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
)

// runBusConformance exercises the subset of event.Bus's contract every
// adapter can honestly guarantee today, regardless of backend: a publish
// with no subscriber is not an error, and a subscriber receives a
// correctly decoded Event for a matching publish.
//
// "Multiple subscribers on the same topic each receive their own
// delivery" is deliberately NOT part of this shared suite as of this
// phase: natsBus.Subscribe now joins every same-topic Subscribe call to
// one shared durable consumer (PLAN.md Section 26.4's Consumer Group
// requirement), so JetStream itself guarantees exactly one of them
// receives a given message, the opposite of fan-out. inProcessBus still
// fans out to every registered handler (it has no consumer-group concept).
// This is a deliberate, spec-required divergence between the two adapters,
// not an oversight: TestInProcessBusFanOutSameTopic (inprocess_test.go)
// proves the in-process fan-out guarantee on its own, and
// TestNatsBusSubscribe_DurableConsumerGroupSplitsMessages
// (nats_consumer_group_test.go) proves the NATS group-splitting guarantee
// on its own.
//
// Every topic used below is prefixed "pleiades.events." to match
// topology.EventSubject's convention, though any subject under
// topology.StreamSubjectRoot ("pleiades.>") would be equally valid; using
// the same prefix for both adapters keeps the suite adapter-agnostic.
//
// newBus must return a Bus ready to accept Publish/Subscribe calls. It may
// return a brand new instance each call or the same already-connected
// instance every time (as the NATS conformance test does, mirroring
// internal/lock/conformance_test.go's identical convention); each subtest
// below uses a topic unique to itself so either style is safe.
func runBusConformance(t *testing.T, newBus func() event.Bus) {
	t.Helper()

	t.Run("PublishBeforeSubscribeIsANoOp", func(t *testing.T) {
		conformancePublishWithNoSubscriber(t, newBus())
	})

	t.Run("SubscribeThenPublishDeliversDecodedEvent", func(t *testing.T) {
		conformanceSubscribeThenPublishDelivers(t, newBus())
	})

	t.Run("HandlerErrorDoesNotPanicOrBlockLaterDeliveries", func(t *testing.T) {
		conformanceHandlerErrorDoesNotBlockLaterDeliveries(t, newBus())
	})
}

// conformancePublishWithNoSubscriber asserts that publishing to a topic with
// no registered subscriber succeeds rather than erroring: Bus.Publish's own
// doc comment describes firing an event "without waiting for a response,"
// which never promised a subscriber exists at all.
func conformancePublishWithNoSubscriber(t *testing.T, bus event.Bus) {
	t.Helper()

	evt := event.Event{ID: "orphan", Type: "conformance.orphan"}
	err := bus.Publish(context.Background(), "pleiades.events.conformance.orphan", evt)
	if err != nil {
		t.Fatalf("publish with no subscriber returned an error: %v", err)
	}
}

// conformanceSubscribeThenPublishDelivers asserts a registered subscriber
// receives a correctly decoded Event, generalizing TestNatsJetStreamBus's
// own assertion so both adapters run the identical sequence.
func conformanceSubscribeThenPublishDelivers(t *testing.T, bus event.Bus) {
	t.Helper()
	const topic = "pleiades.events.conformance.delivery"

	received := make(chan event.Event, 1)
	if err := bus.Subscribe(context.Background(), topic, func(e event.Event) error {
		received <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	evt, err := event.WrapPayload("conformance-delivery-1", "conformance.delivered", map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("wrap payload: %v", err)
	}

	if err := bus.Publish(context.Background(), topic, *evt); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-received:
		if got.ID != "conformance-delivery-1" {
			t.Errorf("delivered event ID = %q, want conformance-delivery-1", got.ID)
		}
		if got.Type != "conformance.delivered" {
			t.Errorf("delivered event Type = %q, want conformance.delivered", got.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for delivery")
	}
}

// conformanceHandlerErrorDoesNotBlockLaterDeliveries asserts that a
// handler returning an error for one delivery does not prevent a later,
// independent publish on the same topic from still being delivered: a
// Bus.Subscribe implementation's failure handling (redelivery, DLQ) must
// not wedge the whole subscription.
func conformanceHandlerErrorDoesNotBlockLaterDeliveries(t *testing.T, bus event.Bus) {
	t.Helper()
	const topic = "pleiades.events.conformance.handler-error"

	// Buffered generously: an adapter that redelivers the failing message
	// (natsBus, via backoff) may enqueue several more deliveries of it
	// before this test's receiving loop below drains them, and the
	// handler must never block on a full channel.
	received := make(chan event.Event, 8)
	if err := bus.Subscribe(context.Background(), topic, func(e event.Event) error {
		received <- e
		if e.ID == "conformance-error-1" {
			return errFail
		}
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	failing, err := event.WrapPayload("conformance-error-1", "conformance.error", nil)
	if err != nil {
		t.Fatalf("wrap failing payload: %v", err)
	}
	if err := bus.Publish(context.Background(), topic, *failing); err != nil {
		t.Fatalf("publish failing event: %v", err)
	}

	select {
	case got := <-received:
		if got.ID != "conformance-error-1" {
			t.Fatalf("expected the failing event first, got %q", got.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the failing event's first delivery")
	}

	succeeding, err := event.WrapPayload("conformance-error-2", "conformance.ok", nil)
	if err != nil {
		t.Fatalf("wrap succeeding payload: %v", err)
	}
	if err := bus.Publish(context.Background(), topic, *succeeding); err != nil {
		t.Fatalf("publish succeeding event: %v", err)
	}

	deadline := time.After(10 * time.Second)
	for {
		select {
		case got := <-received:
			if got.ID == "conformance-error-2" {
				return
			}
			// Anything else (including a redelivery of the failing event)
			// is fine to see and ignore here; this loop only needs to
			// confirm the independent, later publish still arrives.
		case <-deadline:
			t.Fatal("timed out waiting for the later, independent publish to be delivered")
		}
	}
}

// errFail is a fixed sentinel error used by
// conformanceHandlerErrorDoesNotBlockLaterDeliveries so the assertion
// above can identify it is dealing with the deliberately-failing delivery,
// not any other error.
var errFail = errConformanceHandlerFailure{}

type errConformanceHandlerFailure struct{}

func (errConformanceHandlerFailure) Error() string { return "conformance: deliberate handler failure" }

// TestInProcessBusConformance runs the shared adapter conformance suite
// against a fresh NewInProcessBus for every subtest, proving the in-process
// adapter honors the substitutable Bus contract runBusConformance encodes.
func TestInProcessBusConformance(t *testing.T) {
	runBusConformance(t, func() event.Bus {
		return event.NewInProcessBus()
	})
}
