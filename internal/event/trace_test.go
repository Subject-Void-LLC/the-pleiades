package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestTraceContext_RoundTripsThroughHeaders is the unit-level proof of the
// carrier: a span injected into a nats.Header must extract back out as the
// same trace, marked remote so a child span is attributed to the consuming
// process rather than the publishing one.
func TestTraceContext_RoundTripsThroughHeaders(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	ctx, span := tp.Tracer("test").Start(context.Background(), "publish")
	defer span.End()

	hdr := nats.Header{}
	event.InjectTraceContext(ctx, hdr)

	extracted := trace.SpanContextFromContext(event.ExtractTraceContext(context.Background(), hdr))
	if extracted.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("extracted trace ID is %v, want %v", extracted.TraceID(), span.SpanContext().TraceID())
	}
	if extracted.SpanID() != span.SpanContext().SpanID() {
		t.Errorf("extracted parent span ID is %v, want %v", extracted.SpanID(), span.SpanContext().SpanID())
	}
	if !extracted.IsRemote() {
		t.Error("extracted span context is not marked remote")
	}
}

// TestTraceContext_HeaderKeyIsLowercaseAndCaseInsensitive pins the exact
// wire spelling. The W3C specification mandates a lowercase `traceparent`,
// and nats.Header does not canonicalize keys the way http.Header does, so
// a carrier that leaned on the two types sharing an underlying map would
// silently write `Traceparent` and be invisible to any non-Go consumer.
func TestTraceContext_HeaderKeyIsLowercaseAndCaseInsensitive(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	ctx, span := tp.Tracer("test").Start(context.Background(), "publish")
	defer span.End()

	hdr := nats.Header{}
	event.InjectTraceContext(ctx, hdr)

	if _, ok := hdr["traceparent"]; !ok {
		t.Errorf("no exactly-lowercase traceparent key was written; header keys are %v", headerKeys(hdr))
	}

	// A message from a producer that used the canonicalized spelling must
	// still be readable.
	canonical := nats.Header{"Traceparent": hdr["traceparent"]}
	extracted := trace.SpanContextFromContext(event.ExtractTraceContext(context.Background(), canonical))
	if extracted.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("a Traceparent-spelled header did not extract; got trace %v, want %v",
			extracted.TraceID(), span.SpanContext().TraceID())
	}
}

// TestTraceContext_AbsentAndMalformedAreInert proves the two ways a
// message can carry no usable trace context both degrade to "start a new
// trace" rather than erroring or panicking. A malformed traceparent is
// attacker-supplied text on an unauthenticated path in the general case,
// so it must never be load bearing.
func TestTraceContext_AbsentAndMalformedAreInert(t *testing.T) {
	tests := []struct {
		name string
		hdr  nats.Header
	}{
		{name: "nil header", hdr: nil},
		{name: "empty header", hdr: nats.Header{}},
		{name: "garbage traceparent", hdr: nats.Header{"traceparent": []string{"not-a-traceparent"}}},
		{name: "all-zero trace id", hdr: nats.Header{"traceparent": []string{"00-00000000000000000000000000000000-0000000000000000-01"}}},
		{name: "empty value", hdr: nats.Header{"traceparent": []string{""}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := event.ExtractTraceContext(context.Background(), tt.hdr)
			if trace.SpanContextFromContext(ctx).IsValid() {
				t.Error("a header carrying no usable trace context produced a valid span context")
			}
		})
	}
}

// TestTraceContext_InjectIsNoOpWithoutASpan proves a publish made outside
// any trace produces a clean message rather than one carrying a
// half-filled, misleading traceparent.
func TestTraceContext_InjectIsNoOpWithoutASpan(t *testing.T) {
	hdr := nats.Header{}
	event.InjectTraceContext(context.Background(), hdr)
	if len(hdr) != 0 {
		t.Errorf("injecting with no active span wrote %v, want an empty header", hdr)
	}
}

// TestNatsBus_PublishCarriesTraceContextOnTheWire is the Section 19 proof
// this phase owes, against the real adapter and a real broker rather than
// the carrier in isolation: after a real Publish inside a real span, the
// message sitting in the stream must carry W3C trace context that extracts
// back to that same trace.
//
// It reads the message with a plain JetStream consumer, not through
// Bus.Subscribe, precisely because Bus.Subscribe's handler signature takes
// no context and so cannot see headers at all. That is a real, named gap:
// the one production consumer that does read them is runner.Agent, which
// pulls raw jetstream.Msg values on its own loop.
func TestNatsBus_PublishCarriesTraceContextOnTheWire(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url, nil)
	if err != nil {
		t.Fatalf("failed to init nats bus: %v", err)
	}
	t.Cleanup(func() {
		if err := bus.Close(); err != nil {
			t.Errorf("closing bus: %v", err)
		}
	})

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	subject := topology.DispatchSubject()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connecting a reader: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("getting jetstream: %v", err)
	}
	consumer, err := js.CreateOrUpdateConsumer(ctx, topology.StreamName, jetstream.ConsumerConfig{
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("creating reader consumer: %v", err)
	}

	publishCtx, span := tp.Tracer("test").Start(ctx, "api-request")
	evt, err := event.WrapPayload("evt-1", "runbook.dispatched", map[string]string{"device": "sw1"})
	if err != nil {
		t.Fatalf("wrapping payload: %v", err)
	}
	if err := bus.Publish(publishCtx, subject, *evt); err != nil {
		t.Fatalf("publishing: %v", err)
	}
	span.End()

	msgs, err := consumer.Fetch(1, jetstream.FetchMaxWait(10*time.Second))
	if err != nil {
		t.Fatalf("fetching: %v", err)
	}
	var msg jetstream.Msg
	for m := range msgs.Messages() {
		msg = m
	}
	if msgs.Error() != nil {
		t.Fatalf("fetch stream error: %v", msgs.Error())
	}
	if msg == nil {
		t.Fatal("no message arrived within the fetch window")
	}

	// The trace must be reconstructible by a consumer that has parsed
	// nothing but the headers.
	consumed := trace.SpanContextFromContext(event.ExtractTraceContext(context.Background(), msg.Headers()))
	if consumed.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("the message's trace context names trace %v, want the publisher's %v",
			consumed.TraceID(), span.SpanContext().TraceID())
	}
	if !consumed.IsRemote() {
		t.Error("the extracted span context is not marked remote")
	}
	if err := msg.Ack(); err != nil {
		t.Errorf("acking: %v", err)
	}
}

// headerKeys lists a header's keys, for a failure message that shows what
// was actually written.
func headerKeys(hdr nats.Header) []string {
	keys := make([]string, 0, len(hdr))
	for k := range hdr {
		keys = append(keys, k)
	}
	return keys
}
