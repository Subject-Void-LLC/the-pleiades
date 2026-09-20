package api_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers what survives onto the publish context: the trace the
// launch happened under.
//
// It is worth its own file because the mechanism is deliberately odd.
// publishRequested builds a BACKGROUND context, so a client disconnecting
// cannot cancel a launch that has already been durably persisted, and then
// grafts the actor, the trace id and the live span back onto it explicitly.
// Every one of those is an easy thing to drop in a refactor and none of
// them fails anything visibly: the launch still works, the trace just stops
// at the bus boundary and the audit envelope loses who caused it.

func TestLaunchTemplate_PropagatesTheTraceItWasLaunchedUnder(t *testing.T) {
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), bus,
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := tp.Shutdown(t.Context()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	ctx, span := tp.Tracer("test").Start(context.Background(), "test-request")
	defer span.End()
	wantTraceID := span.SpanContext().TraceID().String()

	if _, _, err := dispatcher.LaunchTemplate(ctx, "ada@example.com", 12, launch.Config{}, nil); err != nil {
		t.Fatalf("LaunchTemplate: %v", err)
	}
	if got := bus.count(); got != 1 {
		t.Fatalf("publishes = %d, want 1", got)
	}

	gotTraceID, ok := event.TraceIDFromContext(bus.lastContext())
	if !ok || gotTraceID != wantTraceID {
		t.Errorf("the publish context carries TraceID (%q, %v), want %q", gotTraceID, ok, wantTraceID)
	}
	// The live span must survive onto the publish context too, not just
	// its id: without it the Bus adapter has nothing to inject and the
	// trace stops at the bus boundary.
	if got := trace.SpanContextFromContext(bus.lastContext()).TraceID().String(); got != wantTraceID {
		t.Errorf("the publish context carries the span of trace %q, want the live one %q", got, wantTraceID)
	}
	// And the actor, which is the other thing grafted onto a background
	// context that inherits nothing.
	if got, ok := event.ActorFromContext(bus.lastContext()); !ok || got != "ada@example.com" {
		t.Errorf("the publish context carries actor (%q, %v), want the launching subject", got, ok)
	}
}

func TestLaunchTemplate_FabricatesNoTraceWhenThereIsNone(t *testing.T) {
	bus := newCapturingBus()
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), bus,
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{}, nil); err != nil {
		t.Fatalf("LaunchTemplate: %v", err)
	}
	if got := bus.count(); got != 1 {
		t.Fatalf("publishes = %d, want 1", got)
	}
	if _, ok := event.TraceIDFromContext(bus.lastContext()); ok {
		t.Error("the publish context carries a TraceID for a launch that happened under no trace")
	}
}

// TestLaunchTemplate_JobIDsAreTimeOrdered is the regression test for a
// template launch minting a random v4 job id. GET /jobs lists newest first
// by ordering on the id and pages with it as a keyset cursor, which only
// works for the time-ordered v7 ids the job schema defaults to, so a v4
// put a new job anywhere in the list. Each launch's id must be a version 7
// UUID, and a later launch's must sort after an earlier one's.
func TestLaunchTemplate_JobIDsAreTimeOrdered(t *testing.T) {
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	var ids []string
	for range 3 {
		id, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{}, nil)
		if err != nil {
			t.Fatalf("LaunchTemplate: %v", err)
		}
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.Version() != 7 {
			t.Fatalf("job id %q is not a version 7 UUID (%v)", id, err)
		}
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Errorf("job id %q, launched after %q, does not sort after it", ids[i], ids[i-1])
		}
	}
}
