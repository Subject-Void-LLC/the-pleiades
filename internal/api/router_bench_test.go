package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// BenchmarkAPIMiddleware measures the full Front Controller chain
// (tracing, metrics, structured logging, panic recovery) on a request that
// does no work of its own, so the number is the pipeline's own overhead
// and nothing else.
//
// The tracer is a real SDK provider with no exporter, which is exactly
// what a deployment with no collector runs: spans are created and sampled
// but never serialized. Benchmarking against a no-op tracer instead would
// measure a configuration nobody deploys.
func BenchmarkAPIMiddleware(b *testing.B) {
	tp := sdktrace.NewTracerProvider()
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			b.Errorf("shutting down tracer provider: %v", err)
		}
	}()

	router, err := api.NewRouter(api.RouterConfig{
		Logger:               slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Tracer:               tp.Tracer("bench"),
		Propagator:           telemetry.Propagator(),
		Registry:             prometheus.NewRegistry(),
		AllowUnauthenticated: true,
	})
	if err != nil {
		b.Fatalf("NewRouter: %v", err)
	}
	req := httptest.NewRequest("GET", "/healthz", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}
}
