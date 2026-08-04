// This file uses package event, not the event_test convention seen in
// nats_test.go, because subjectMatches is an intentionally unexported pure
// function and its table-driven tests need direct access to it. The
// behavioral tests below exercise only the exported Bus, Event, and
// NewInProcessBus surface, so they would also work from event_test; they
// stay here so subjectMatches's unit tests and the bus's behavioral tests
// live together, as the task calls for.
package event

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSubjectMatches table-drives subjectMatches across exact matches,
// trailing wildcard matches, non-matches, and edge cases around empty
// strings and the bare ">" pattern.
func TestSubjectMatches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		topic   string
		want    bool
	}{
		{
			name:    "exact match",
			pattern: "pleiades.events.device.created",
			topic:   "pleiades.events.device.created",
			want:    true,
		},
		{
			name:    "exact non-match different token",
			pattern: "pleiades.events.device.created",
			topic:   "pleiades.events.device.deleted",
			want:    false,
		},
		{
			name:    "wildcard matches single extra token",
			pattern: "pleiades.events.>",
			topic:   "pleiades.events.foo",
			want:    true,
		},
		{
			name:    "wildcard matches multiple extra tokens",
			pattern: "pleiades.events.>",
			topic:   "pleiades.events.foo.bar",
			want:    true,
		},
		{
			name:    "wildcard does not match prefix alone",
			pattern: "pleiades.events.>",
			topic:   "pleiades.events",
			want:    false,
		},
		{
			name:    "wildcard requires at least one token",
			pattern: "pleiades.events.>",
			topic:   "pleiades.events.",
			// Splitting "pleiades.events." on "." yields a trailing empty
			// token, which counts as one token beyond the prefix. This is a
			// deliberate consequence of token-based matching, not a special
			// case: an empty trailing token is still a token.
			want: true,
		},
		{
			name:    "wildcard prefix mismatch does not match",
			pattern: "pleiades.events.>",
			topic:   "pleiades.metrics.foo",
			want:    false,
		},
		{
			name:    "bare wildcard matches any single-token topic",
			pattern: ">",
			topic:   "foo",
			want:    true,
		},
		{
			name:    "bare wildcard matches multi-token topic",
			pattern: ">",
			topic:   "foo.bar.baz",
			want:    true,
		},
		{
			name:    "empty pattern and empty topic are an exact match",
			pattern: "",
			topic:   "",
			want:    true,
		},
		{
			name:    "empty pattern does not match non-empty topic",
			pattern: "",
			topic:   "pleiades.events.foo",
			want:    false,
		},
		{
			name:    "non-empty pattern does not match empty topic",
			pattern: "pleiades.events.>",
			topic:   "",
			want:    false,
		},
		{
			name:    "greater-than mid-pattern is treated as a literal token",
			pattern: "pleiades.>.events",
			topic:   "pleiades.foo.events",
			// The wildcard token must be the last token to trigger wildcard
			// matching. Here it is not, so only an exact match qualifies,
			// and this is not one.
			want: false,
		},
		{
			name:    "greater-than mid-pattern exact match still works",
			pattern: "pleiades.>.events",
			topic:   "pleiades.>.events",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subjectMatches(tt.pattern, tt.topic)
			if got != tt.want {
				t.Errorf("subjectMatches(%q, %q) = %v, want %v", tt.pattern, tt.topic, got, tt.want)
			}
		})
	}
}

// TestInProcessBusPublishBeforeSubscribe verifies that publishing to a bus
// with no subscribers is a silent no-op: no error and no panic.
func TestInProcessBusPublishBeforeSubscribe(t *testing.T) {
	bus := NewInProcessBus()

	evt := Event{ID: "no-subscribers", Type: "device.created"}

	// Nothing is subscribed yet, so this must not error or panic even
	// though there is no one to deliver to.
	if err := bus.Publish(context.Background(), "pleiades.events.device.created", evt); err != nil {
		t.Fatalf("unexpected error publishing with no subscribers: %v", err)
	}
}

// TestInProcessBusDeliversDecodedEvent verifies that a subscriber on an
// exact topic receives the same Event that was published, ID, Type, and
// Data intact.
func TestInProcessBusDeliversDecodedEvent(t *testing.T) {
	bus := NewInProcessBus()

	// Buffered by one so the handler goroutine never blocks on send even if
	// the test is slow to reach the receive below.
	received := make(chan Event, 1)
	err := bus.Subscribe(context.Background(), "pleiades.events.device.created", func(evt Event) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	wantEvt, err := WrapPayload("uuid-123", "device.created", map[string]string{"ip": "10.0.0.5"})
	if err != nil {
		t.Fatalf("failed to wrap payload: %v", err)
	}

	if err := bus.Publish(context.Background(), "pleiades.events.device.created", *wantEvt); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// Synchronize with the async handler goroutine via the channel rather
	// than sleeping.
	select {
	case gotEvt := <-received:
		if gotEvt.ID != "uuid-123" {
			t.Errorf("expected ID uuid-123, got %s", gotEvt.ID)
		}
		if gotEvt.Type != "device.created" {
			t.Errorf("expected Type device.created, got %s", gotEvt.Type)
		}
		if string(gotEvt.Data) != string(wantEvt.Data) {
			t.Errorf("expected Data %s, got %s", wantEvt.Data, gotEvt.Data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event delivery")
	}
}

// TestInProcessBusFanOutSameTopic verifies that multiple subscribers
// registered on the exact same topic all receive an independent delivery of
// a single Publish. Unlike natsBus (which groups same-topic Subscribe calls
// into one shared durable consumer per PLAN.md Section 26.4), the
// in-process adapter has no consumer-group concept and always fans out;
// see conformance_test.go's own doc comment for why this guarantee is
// tested here rather than in the shared adapter conformance suite.
func TestInProcessBusFanOutSameTopic(t *testing.T) {
	bus := NewInProcessBus()

	const subscriberCount = 3
	received := make(chan string, subscriberCount)
	for i := 0; i < subscriberCount; i++ {
		err := bus.Subscribe(context.Background(), "pleiades.events.device.created", func(evt Event) error {
			received <- evt.ID
			return nil
		})
		if err != nil {
			t.Fatalf("failed to subscribe: %v", err)
		}
	}

	evt := Event{ID: "fan-out-1", Type: "device.created"}
	if err := bus.Publish(context.Background(), "pleiades.events.device.created", evt); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// Every subscriber must independently receive the same event.
	for i := 0; i < subscriberCount; i++ {
		select {
		case gotID := <-received:
			if gotID != "fan-out-1" {
				t.Errorf("subscriber %d: expected ID fan-out-1, got %s", i, gotID)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for subscriber %d to receive delivery", i)
		}
	}
}

// TestInProcessBusWildcardSubscriber verifies that a subscriber registered
// on a trailing-wildcard pattern receives a publish on a matching concrete
// topic, and does not receive one on a non-matching topic.
func TestInProcessBusWildcardSubscriber(t *testing.T) {
	bus := NewInProcessBus()

	received := make(chan Event, 1)
	err := bus.Subscribe(context.Background(), "pleiades.events.>", func(evt Event) error {
		received <- evt
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	// A publish on a matching topic must be delivered.
	matchEvt := Event{ID: "match-1", Type: "device.created"}
	if err := bus.Publish(context.Background(), "pleiades.events.device.created", matchEvt); err != nil {
		t.Fatalf("failed to publish matching event: %v", err)
	}

	select {
	case gotEvt := <-received:
		if gotEvt.ID != "match-1" {
			t.Errorf("expected ID match-1, got %s", gotEvt.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for matching event delivery")
	}

	// A publish on a non-matching topic (different prefix) must not be
	// delivered. Follow it with a matching publish and confirm only the
	// matching one arrives, proving the non-matching publish was truly
	// dropped rather than merely delayed.
	nonMatchEvt := Event{ID: "non-match-1", Type: "unrelated"}
	if err := bus.Publish(context.Background(), "pleiades.metrics.cpu", nonMatchEvt); err != nil {
		t.Fatalf("failed to publish non-matching event: %v", err)
	}

	sentinelEvt := Event{ID: "sentinel-1", Type: "device.created"}
	if err := bus.Publish(context.Background(), "pleiades.events.device.created", sentinelEvt); err != nil {
		t.Fatalf("failed to publish sentinel event: %v", err)
	}

	select {
	case gotEvt := <-received:
		if gotEvt.ID != "sentinel-1" {
			t.Errorf("expected only the sentinel event to arrive, got ID %s (non-matching topic was delivered)", gotEvt.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sentinel event delivery")
	}
}

// TestInProcessBusHandlerErrorIsNotFatal verifies that a handler returning
// an error is tolerated (logged, per inProcessBus's own documented
// limitation) rather than panicking or breaking later deliveries on the
// same subscription.
func TestInProcessBusHandlerErrorIsNotFatal(t *testing.T) {
	bus := NewInProcessBus()

	calls := make(chan Event, 2)
	err := bus.Subscribe(context.Background(), "pleiades.events.device.created", func(evt Event) error {
		calls <- evt
		if evt.ID == "first-fails" {
			return errors.New("deliberate failure")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), "pleiades.events.device.created", Event{ID: "first-fails"}); err != nil {
		t.Fatalf("failed to publish first event: %v", err)
	}
	if err := bus.Publish(context.Background(), "pleiades.events.device.created", Event{ID: "second-succeeds"}); err != nil {
		t.Fatalf("failed to publish second event: %v", err)
	}

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case evt := <-calls:
			seen[evt.ID] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for call %d; seen so far: %v", i, seen)
		}
	}
	if !seen["first-fails"] || !seen["second-succeeds"] {
		t.Errorf("expected both events to reach the handler, got %v", seen)
	}
}

// TestInProcessBusRespectsCanceledContext verifies that Publish and
// Subscribe both fail fast with a wrapped context error when called with an
// already-canceled context, rather than proceeding as if nothing were
// wrong.
func TestInProcessBusRespectsCanceledContext(t *testing.T) {
	bus := NewInProcessBus()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so ctx.Err() is non-nil below

	if err := bus.Subscribe(ctx, "pleiades.events.device.created", func(evt Event) error { return nil }); err == nil {
		t.Error("expected Subscribe to return an error for a canceled context")
	}

	if err := bus.Publish(ctx, "pleiades.events.device.created", Event{}); err == nil {
		t.Error("expected Publish to return an error for a canceled context")
	}
}
