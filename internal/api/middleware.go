// Package api is the Front Controller for the Pleiades control plane: one
// chi router owns routing and every cross-cutting concern (tracing,
// structured logging, RED metrics, panic recovery, rate limiting, auth) so
// no individual handler can accidentally opt out of telemetry.
//
// Every middleware here takes its dependencies as arguments. An earlier
// shape of this package kept a package-level slog.Logger and registered
// its Prometheus collectors into the default registry at init time, which
// made the request pipeline impossible to configure per process and forced
// every test in the package to share one mutable global.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// contextKey is a private type so this package's context keys can never
// collide with a key any other package defines, even one that also uses a
// bare string.
type contextKey string

const identityKey contextKey = "identity"

// traceIDHeader is the response header carrying the trace ID of the span
// that served a request, so an operator holding an HTTP response can find
// the trace it belongs to without reading logs first.
//
// Correction (2026-08-06) to PATTERNS.md's Correlation ID entry, which
// described X-Trace-ID as both the inbound and outbound spelling: inbound
// trace context is now W3C `traceparent`, because a bare trace ID is not
// enough to continue a trace (a child span needs its parent's span ID and
// the sampling flag too, which a lone ID cannot carry). X-Trace-ID
// survives as an outbound convenience only.
const traceIDHeader = "X-Trace-ID"

// unmatchedRoute is the route label used for a request no route pattern
// matched. Falling back to the raw URL path here would make the metric's
// label set unbounded, which is how a scraped counter turns into a
// cardinality incident: anyone can invent an infinite number of 404 paths.
const unmatchedRoute = "unmatched"

// TraceIDFromContext returns the trace ID of the span currently recording
// in ctx. It reports false when no span is recording or the span context
// carries no valid trace ID, so a caller can tell "tracing is off" apart
// from "the trace ID happens to be all zeros".
func TraceIDFromContext(ctx context.Context) (string, bool) {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.HasTraceID() {
		return "", false
	}
	return sc.TraceID().String(), true
}

// IdentityFromContext returns the authenticated identity AuthMiddleware
// placed in ctx, if any.
func IdentityFromContext(ctx context.Context) (*auth.Identity, bool) {
	id, ok := ctx.Value(identityKey).(*auth.Identity)
	return id, ok && id != nil
}

// TracingMiddleware starts one OpenTelemetry server span per request,
// continuing whatever trace the caller propagated in its headers.
//
// This replaces a middleware that read an X-Trace-ID header or minted a
// UUID. That produced an identifier but never a span, so nothing was ever
// exported, nothing had a duration, and nothing linked a Controller
// request to the Runner work it caused: PLAN.md Section 19's whole
// requirement. The span is named for the matched chi route pattern, not
// the raw path, for the same cardinality reason the metrics labels are.
// Because chi only knows the pattern after routing, the span starts under
// the bare HTTP method and is renamed on the way out.
func TracingMiddleware(tracer trace.Tracer, propagator propagation.TextMapPropagator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := tracer.Start(ctx, r.Method,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.request.method", r.Method),
					attribute.String("url.path", r.URL.Path),
					attribute.String("network.protocol.name", "http"),
				),
			)
			defer span.End()

			if sc := span.SpanContext(); sc.HasTraceID() {
				w.Header().Set(traceIDHeader, sc.TraceID().String())
			}

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r.WithContext(ctx))

			route := routePattern(r)
			span.SetName(r.Method + " " + route)
			span.SetAttributes(
				attribute.String("http.route", route),
				attribute.Int("http.response.status_code", ww.Status()),
			)
			// Only 5xx marks the span as failed. A 4xx is the client
			// getting the answer it asked for, and marking it an error
			// makes every trace dashboard's error rate track how often
			// callers send bad requests rather than how often this service
			// is broken.
			if ww.Status() >= http.StatusInternalServerError {
				span.SetStatus(codes.Error, http.StatusText(ww.Status()))
			}
		})
	}
}

// StructuredLoggerMiddleware emits one JSON log line per completed
// request, carrying the trace ID of the span TracingMiddleware started.
// It logs on completion only: the "request started" line the previous
// implementation also wrote doubled log volume while carrying no field the
// completion line does not already have.
func StructuredLoggerMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r.WithContext(r.Context()))

			attrs := []any{
				slog.String("method", r.Method),
				slog.String("route", routePattern(r)),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Int("bytes", ww.BytesWritten()),
				slog.Duration("latency", time.Since(start)),
			}
			if traceID, ok := TraceIDFromContext(r.Context()); ok {
				attrs = append(attrs, slog.String("trace_id", traceID))
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request completed", toAttrs(attrs)...)
		})
	}
}

// toAttrs narrows the []any slog.Attr values built above back to the typed
// slice LogAttrs wants. LogAttrs is used rather than Info so slog does no
// reflection over alternating key/value pairs on the hot request path.
func toAttrs(values []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(values))
	for _, v := range values {
		if a, ok := v.(slog.Attr); ok {
			attrs = append(attrs, a)
		}
	}
	return attrs
}

// MetricsMiddleware records the RED signals (Rate, Errors, Duration) for
// every request into m.
func MetricsMiddleware(m *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			m.inFlight.Inc()
			start := time.Now()

			// The in-flight gauge is decremented via defer, not after the
			// call, so a panic that middleware.Recoverer catches upstream
			// cannot leave the gauge permanently incremented.
			defer func() {
				m.inFlight.Dec()
				m.observe(routePattern(r), r.Method, ww.Status(), time.Since(start))
			}()

			next.ServeHTTP(ww, r)
		})
	}
}

// routePattern returns the chi route pattern that matched r, which is the
// bounded-cardinality label ("/api/v1/jobs/{id}/logs") rather than the raw
// path ("/api/v1/jobs/9f3c.../logs"). It returns unmatchedRoute when no
// route matched, so a 404 flood cannot mint new label values.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return unmatchedRoute
	}
	if pattern := rctx.RoutePattern(); pattern != "" {
		return pattern
	}
	return unmatchedRoute
}

// TokenValidator is the narrow slice of auth.Evaluator AuthMiddleware
// needs. Depending on the method it actually calls, rather than the whole
// evaluator, is what lets a test supply a two-line double.
type TokenValidator interface {
	ValidateToken(ctx context.Context, tokenStr string) (*auth.Identity, error)
}

// bearerPrefix is the exact, case-sensitive scheme prefix an Authorization
// header must carry.
const bearerPrefix = "Bearer "

// AuthMiddleware rejects any request without a valid Bearer token and
// places the resolved identity in the request context for downstream
// handlers and for the rate limiter's per-identity keying.
func AuthMiddleware(validator TokenValidator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if len(authHeader) <= len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			identity, err := validator.ValidateToken(r.Context(), authHeader[len(bearerPrefix):])
			if err != nil {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), identityKey, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// IdentityKeyForTest exposes the internal identity key so a test in
// another package can build an already-authenticated request without
// standing up a token issuer.
//
// This is the production test hook Phase 12's own checklist owns removing
// (it lets any caller, including non-test code, bypass AuthMiddleware
// entirely). It survives this phase unchanged and deliberately: moving it
// behind an export_test.go seam is not enough on its own, because
// tests/e2e is a different package and would lose access, so the real fix
// is a test-only token issuer that Phase 12 is the right place to build.
//
// Its sibling TraceIDKeyForTest is gone, forced by this phase rather than
// chosen: there is no longer a trace ID context key to expose. The trace
// ID now comes from the OpenTelemetry span context, so a test seeds it by
// starting a real span (see TraceIDFromContext).
const IdentityKeyForTest = identityKey
