// Package api_test: a request's method reaches a metric label and a span
// only as one of a fixed set.
package api_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestRouter_MethodLabelIsBounded holds the HTTP method to the bounded
// identifiers telemetry may carry. Go's server accepts any token as a
// method, and the method was a metric label and a span attribute as sent,
// so anyone who could reach the port, before authentication, could mint a
// new label set per request and put text of their choosing on every
// collector's spans. An unknown method is recorded as "_OTHER", the
// OpenTelemetry convention, and a known one as itself.
func TestRouter_MethodLabelIsBounded(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var logBuf bytes.Buffer
	router, reg := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Tracer = tp.Tracer("methods")
		cfg.Routes = []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs/{id}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}},
		}
	})

	get(t, router, "BREWCANARYONE", "/api/v1/jobs/aaaaaaaa")
	get(t, router, "BREWCANARYTWO", "/nope")
	get(t, router, http.MethodGet, "/api/v1/jobs/aaaaaaaa")

	for _, label := range labelValues(t, reg, "http_requests_total", "method") {
		if label != "_OTHER" && label != http.MethodGet {
			t.Errorf("http_requests_total carries the method label %q, want only %q and %q", label, "_OTHER", http.MethodGet)
		}
	}
	// Both unknown methods land on the one bounded label: one request to a
	// route that does not take the method, one to no route at all.
	var other float64
	for _, route := range []string{"/api/v1/jobs/{id}", "unmatched"} {
		for _, code := range []string{"404", "405"} {
			other += counterValue(t, reg, "http_requests_total", map[string]string{"route": route, "method": "_OTHER", "code": code})
		}
	}
	if other != 2 {
		t.Errorf("http_requests_total counts %v requests under method _OTHER, want 2", other)
	}

	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("recorded %d spans, want 3", len(spans))
	}
	for _, span := range spans[:2] {
		for _, attr := range span.Attributes() {
			if attr.Key == "http.request.method" && attr.Value.AsString() != "_OTHER" {
				t.Errorf("span http.request.method is %q, want %q", attr.Value.AsString(), "_OTHER")
			}
		}
		if name := span.Name(); name != "_OTHER /api/v1/jobs/{id}" && name != "_OTHER unmatched" {
			t.Errorf("span name is %q, want the bounded method and the route pattern", name)
		}
	}
	if name := spans[2].Name(); name != "GET /api/v1/jobs/{id}" {
		t.Errorf("a known method's span name is %q, want %q", name, "GET /api/v1/jobs/{id}")
	}
}
