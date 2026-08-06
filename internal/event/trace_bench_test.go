package event_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/nats-io/nats.go"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// BenchmarkInjectTraceContext measures the per-publish cost this phase
// adds to every message on the bus.
func BenchmarkInjectTraceContext(b *testing.B) {
	tp := sdktrace.NewTracerProvider()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			b.Errorf("shutting down tracer provider: %v", err)
		}
	}()
	ctx, span := tp.Tracer("bench").Start(context.Background(), "publish")
	defer span.End()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hdr := nats.Header{}
		event.InjectTraceContext(ctx, hdr)
	}
}

// BenchmarkExtractTraceContext measures the per-delivery cost on the
// consuming side.
func BenchmarkExtractTraceContext(b *testing.B) {
	hdr := nats.Header{"traceparent": []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event.ExtractTraceContext(ctx, hdr)
	}
}
