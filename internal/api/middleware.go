// Package api is the Front Controller for The Pleiades control plane: one
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

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
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

// loggerKey carries the request-scoped *slog.Logger
// StructuredLoggerMiddleware installs, so a handler can report a
// server-side failure through the logger the router was configured with
// rather than through the process-wide default.
const loggerKey contextKey = "logger"

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

// loggerFromContext returns the request-scoped logger
// StructuredLoggerMiddleware placed in ctx, if any. Absence is not an
// error: a handler a test invokes directly still has to be able to
// respond, and loggerFrom (respond.go) supplies the fallback.
func loggerFromContext(ctx context.Context) (*slog.Logger, bool) {
	logger, ok := ctx.Value(loggerKey).(*slog.Logger)
	return logger, ok && logger != nil
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
// It also places logger in the request context. A handler that needs to
// report a server-side failure it cannot tell the client about (Respond's
// own marshal and short-write branches, respond.go) would otherwise have
// to reach for slog.Default(), which is the process-wide logger the
// router was explicitly configured not to assume.
func StructuredLoggerMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()

			next.ServeHTTP(ww, r.WithContext(context.WithValue(r.Context(), loggerKey, logger)))

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

// CredentialSource resolves one kind of credential off a request.
//
// It exists because a browser cannot present a Bearer token: an
// EventSource cannot set headers at all, so the SSE job-log stream was
// unreachable from any browser client until a second credential kind
// existed. The alternative to this interface was for the web UI to write
// its own identity into its own context key, which cannot work --
// identityKey is unexported, so nothing outside this package can put an
// identity where IdentityFromContext will find it. Generalizing here,
// rather than opening a second door, keeps one place writing identity into
// a request and one place answering 401.
type CredentialSource interface {
	// Name identifies this source in logs.
	Name() string

	// Resolve returns the identity this request carries for this kind of
	// credential.
	//
	// It returns (nil, nil) when the request carries no credential of
	// this kind at all, which is deliberately distinct from returning an
	// error. "Not my kind of request" must fall through to the next
	// source; "a credential of my kind, and it is bad" must not.
	Resolve(r *http.Request) (*auth.Identity, error)
}

// BearerSource resolves an Authorization: Bearer token. It is the
// credential kind every API and CLI client uses, and its behaviour is
// unchanged from when it was the only one.
type BearerSource struct {
	Validator TokenValidator
}

// Name implements CredentialSource.
func (BearerSource) Name() string { return "bearer" }

// Resolve implements CredentialSource.
func (s BearerSource) Resolve(r *http.Request) (*auth.Identity, error) {
	authHeader := r.Header.Get("Authorization")
	if len(authHeader) <= len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
		// No Bearer credential present. Not an error: another source may
		// still authenticate this request.
		return nil, nil
	}
	return s.Validator.ValidateToken(r.Context(), authHeader[len(bearerPrefix):])
}

// IdentityMiddleware authenticates a request against each source in turn
// and places the resolved identity in the request context, for downstream
// handlers and for the rate limiter's per-identity keying.
//
// Source order is significant and is the caller's to choose. Bearer must
// come before any ambient credential: an explicit Authorization header is
// an unambiguous statement of intent, while a cookie is sent by the
// browser whether or not the caller meant it, so trying the cookie first
// would let a stale session silently override a token a caller took the
// trouble to supply.
//
// unauthorized renders the failure. It is a parameter because the two
// subtrees need different answers to the same condition: the JSON API owes
// an unauthenticated caller 401 with a machine-readable body, while the UI
// owes a browser a redirect to its login page. A single hardcoded response
// would make one of the two wrong. A nil unauthorized falls back to the
// API's 401.
func IdentityMiddleware(unauthorized http.HandlerFunc, sources ...CredentialSource) func(http.Handler) http.Handler {
	if unauthorized == nil {
		unauthorized = func(w http.ResponseWriter, r *http.Request) {
			RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, source := range sources {
				identity, err := source.Resolve(r)
				if err != nil {
					// A credential of this kind was presented and is not
					// valid. Falling through to the next source here
					// would let a bad token be rescued by an ambient
					// cookie, which is the opposite of what presenting a
					// token means.
					unauthorized(w, r)
					return
				}
				if identity == nil {
					continue
				}
				ctx := context.WithValue(r.Context(), identityKey, identity)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			unauthorized(w, r)
		})
	}
}

// AuthMiddleware rejects any request without a valid Bearer token and
// places the resolved identity in the request context.
//
// It is kept verbatim as a one-line wrapper so every existing caller and
// test is untouched by the generalization above.
func AuthMiddleware(validator TokenValidator) func(http.Handler) http.Handler {
	return IdentityMiddleware(nil, BearerSource{Validator: validator})
}
