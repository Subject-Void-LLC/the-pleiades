package event_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

func TestContextHelpers_RoundTrip(t *testing.T) {
	ctx := context.Background()

	ctx = event.WithCorrelationID(ctx, "corr-1")
	ctx = event.WithCausationID(ctx, "cause-1")
	ctx = event.WithChainDepth(ctx, 3)
	ctx = event.WithActor(ctx, "user-1")
	ctx = event.WithTraceID(ctx, "trace-1")
	ctx = event.WithIdempotencyKey(ctx, "idem-1")

	if got, ok := event.CorrelationIDFromContext(ctx); !ok || got != "corr-1" {
		t.Errorf("CorrelationIDFromContext = (%q, %v), want (\"corr-1\", true)", got, ok)
	}
	if got, ok := event.CausationIDFromContext(ctx); !ok || got != "cause-1" {
		t.Errorf("CausationIDFromContext = (%q, %v), want (\"cause-1\", true)", got, ok)
	}
	if got, ok := event.ChainDepthFromContext(ctx); !ok || got != 3 {
		t.Errorf("ChainDepthFromContext = (%d, %v), want (3, true)", got, ok)
	}
	if got, ok := event.ActorFromContext(ctx); !ok || got != "user-1" {
		t.Errorf("ActorFromContext = (%q, %v), want (\"user-1\", true)", got, ok)
	}
	if got, ok := event.TraceIDFromContext(ctx); !ok || got != "trace-1" {
		t.Errorf("TraceIDFromContext = (%q, %v), want (\"trace-1\", true)", got, ok)
	}
	if got, ok := event.IdempotencyKeyFromContext(ctx); !ok || got != "idem-1" {
		t.Errorf("IdempotencyKeyFromContext = (%q, %v), want (\"idem-1\", true)", got, ok)
	}
}

func TestContextHelpers_AbsentByDefault(t *testing.T) {
	ctx := context.Background()

	if _, ok := event.CorrelationIDFromContext(ctx); ok {
		t.Error("CorrelationIDFromContext found a value on a bare context")
	}
	if _, ok := event.CausationIDFromContext(ctx); ok {
		t.Error("CausationIDFromContext found a value on a bare context")
	}
	if _, ok := event.ChainDepthFromContext(ctx); ok {
		t.Error("ChainDepthFromContext found a value on a bare context")
	}
	if _, ok := event.ActorFromContext(ctx); ok {
		t.Error("ActorFromContext found a value on a bare context")
	}
	if _, ok := event.TraceIDFromContext(ctx); ok {
		t.Error("TraceIDFromContext found a value on a bare context")
	}
	if _, ok := event.IdempotencyKeyFromContext(ctx); ok {
		t.Error("IdempotencyKeyFromContext found a value on a bare context")
	}
}

func TestChained_RootEventBecomesCorrelationRoot(t *testing.T) {
	// received itself has no CorrelationID (it is a chain root): Chained
	// must use its own ID as the new chain's CorrelationID.
	received := event.Event{ID: "root-event-id", ChainDepth: 0}

	ctx := event.Chained(context.Background(), received)

	gotCorrelation, ok := event.CorrelationIDFromContext(ctx)
	if !ok || gotCorrelation != "root-event-id" {
		t.Errorf("CorrelationIDFromContext = (%q, %v), want (\"root-event-id\", true)", gotCorrelation, ok)
	}
	gotCausation, ok := event.CausationIDFromContext(ctx)
	if !ok || gotCausation != "root-event-id" {
		t.Errorf("CausationIDFromContext = (%q, %v), want (\"root-event-id\", true)", gotCausation, ok)
	}
	gotDepth, ok := event.ChainDepthFromContext(ctx)
	if !ok || gotDepth != 1 {
		t.Errorf("ChainDepthFromContext = (%d, %v), want (1, true)", gotDepth, ok)
	}
}

func TestChained_ContinuesExistingChain(t *testing.T) {
	// received already belongs to a chain: Chained must carry the existing
	// CorrelationID forward unchanged, not mint a new one.
	received := event.Event{
		ID:            "hop-2-event-id",
		CorrelationID: "chain-root-id",
		ChainDepth:    2,
	}

	ctx := event.Chained(context.Background(), received)

	gotCorrelation, ok := event.CorrelationIDFromContext(ctx)
	if !ok || gotCorrelation != "chain-root-id" {
		t.Errorf("CorrelationIDFromContext = (%q, %v), want (\"chain-root-id\", true)", gotCorrelation, ok)
	}
	gotCausation, ok := event.CausationIDFromContext(ctx)
	if !ok || gotCausation != "hop-2-event-id" {
		t.Errorf("CausationIDFromContext = (%q, %v), want (\"hop-2-event-id\", true)", gotCausation, ok)
	}
	gotDepth, ok := event.ChainDepthFromContext(ctx)
	if !ok || gotDepth != 3 {
		t.Errorf("ChainDepthFromContext = (%d, %v), want (3, true)", gotDepth, ok)
	}
}

// TestPublish_StampsEnvelopeFromContext proves Publish actually reads and
// applies the context helpers above, using the in-process bus so no
// container is required.
func TestPublish_StampsEnvelopeFromContext(t *testing.T) {
	bus := event.NewInProcessBus()

	received := make(chan event.Event, 1)
	if err := bus.Subscribe(context.Background(), "pleiades.events.envelope.test", func(e event.Event) error {
		received <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	ctx := context.Background()
	ctx = event.WithCorrelationID(ctx, "corr-stamped")
	ctx = event.WithCausationID(ctx, "cause-stamped")
	ctx = event.WithChainDepth(ctx, 7)
	ctx = event.WithActor(ctx, "actor-stamped")
	ctx = event.WithTraceID(ctx, "trace-stamped")

	if err := bus.Publish(ctx, "pleiades.events.envelope.test", event.Event{ID: "evt-1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got := <-received
	if got.CorrelationID != "corr-stamped" {
		t.Errorf("CorrelationID = %q, want corr-stamped", got.CorrelationID)
	}
	if got.CausationID != "cause-stamped" {
		t.Errorf("CausationID = %q, want cause-stamped", got.CausationID)
	}
	if got.ChainDepth != 7 {
		t.Errorf("ChainDepth = %d, want 7", got.ChainDepth)
	}
	if got.Actor != "actor-stamped" {
		t.Errorf("Actor = %q, want actor-stamped", got.Actor)
	}
	if got.TraceID != "trace-stamped" {
		t.Errorf("TraceID = %q, want trace-stamped", got.TraceID)
	}
	if got.IdempotencyKey == "" {
		t.Error("IdempotencyKey was not stamped even without a context override")
	}
}

// TestPublish_DefaultsCorrelationIDWhenAbsent proves a publish made with no
// context-supplied CorrelationID gets a fresh, non-empty one: it becomes
// the root of a new chain rather than silently having an empty
// CorrelationID.
func TestPublish_DefaultsCorrelationIDWhenAbsent(t *testing.T) {
	bus := event.NewInProcessBus()

	received := make(chan event.Event, 1)
	if err := bus.Subscribe(context.Background(), "pleiades.events.envelope.default", func(e event.Event) error {
		received <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := bus.Publish(context.Background(), "pleiades.events.envelope.default", event.Event{ID: "evt-1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got := <-received
	if got.CorrelationID == "" {
		t.Error("CorrelationID was left empty; Publish must mint a fresh one when the context carries none")
	}
}
