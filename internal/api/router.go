package api

import (
	"log/slog"
	"net/http"

	"github.com/SubjectVoidLLC/the-pleiades/internal/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// APIVersionPrefix is the one versioned surface every application route
// lives under. PATTERNS.md's API Versioning entry requires it, and its
// Backend for Frontend entry ("NO") names this single versioned surface as
// the reason no per-client gateway is needed.
//
// The operational endpoints below are deliberately not under it. /healthz,
// /readyz, and /metrics are contracts with the orchestrator and the
// scrape agent, not with an API consumer: a Kubernetes probe path and a
// Prometheus scrape path are conventional, and versioning them would break
// every stock chart and scrape config for no benefit, since they carry no
// payload schema that could ever need a v2.
const APIVersionPrefix = "/api/v1"

// RouterConfig is the full set of dependencies the Front Controller needs.
// Every field is optional: NewRouter substitutes a safe default for any
// zero value, so a test can ask for the piece it is testing and ignore the
// rest.
type RouterConfig struct {
	// Logger receives one structured line per completed request. Defaults
	// to slog.Default().
	Logger *slog.Logger

	// Tracer starts the per-request server span. Defaults to a no-op
	// tracer, which still routes correctly but mints no trace IDs, so a
	// process that wants real traces must pass one from
	// internal/telemetry.
	Tracer trace.Tracer

	// Propagator decodes inbound trace context. Defaults to the one
	// telemetry.Propagator defines, which is the single place this
	// platform's wire encoding for trace context is decided.
	Propagator propagation.TextMapPropagator

	// Registry is where RED metrics are registered and what /metrics
	// serves. Defaults to a fresh, private registry, so two routers in one
	// process never collide.
	Registry *prometheus.Registry

	// Readiness is the dependency set /readyz reports on. An empty slice
	// means readiness is equivalent to liveness, which is honest for a
	// process with no external dependencies and wrong for the Controller,
	// so the Controller passes real checks.
	Readiness []ReadinessCheck

	// RateLimiter throttles the versioned API subtree per caller. Nil
	// disables throttling. The operational endpoints are never throttled:
	// rate limiting a liveness probe is how a busy replica gets restarted
	// for being busy.
	RateLimiter *RateLimiter

	// Auth guards the versioned API subtree. Nil leaves it unauthenticated,
	// which is only ever right in a test. It is applied before
	// RateLimiter so the limiter can key on the resolved identity rather
	// than on a source address.
	Auth func(http.Handler) http.Handler

	// Routes registers the application's own handlers. It is called with a
	// router already mounted under APIVersionPrefix and already wrapped in
	// Auth and RateLimiter, which is what makes "every route is versioned,
	// authenticated, and throttled" a structural property rather than a
	// rule each handler has to remember.
	Routes func(r chi.Router)
}

// NewRouter builds the Front Controller: one chi.Mux that owns routing and
// every cross-cutting concern.
//
// The middleware order matters and is not arbitrary. Tracing is outermost
// so every later middleware runs inside the request span and every log
// line can carry its trace ID. Metrics is next so it observes the status
// code the Recoverer writes for a panic, rather than never running at all.
// The logger is next for the same reason. Recoverer is innermost of the
// four, so a panic in a handler becomes a 500 that tracing, metrics, and
// logging all see, instead of a torn connection that none of them record.
func NewRouter(cfg RouterConfig) *chi.Mux {
	cfg.applyDefaults()

	r := chi.NewRouter()
	r.Use(TracingMiddleware(cfg.Tracer, cfg.Propagator))
	r.Use(MetricsMiddleware(NewMetrics(cfg.Registry)))
	r.Use(StructuredLoggerMiddleware(cfg.Logger))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", healthzHandler)
	r.Get("/readyz", readyzHandler(cfg.Logger, cfg.Readiness))
	r.Handle("/metrics", promhttp.HandlerFor(cfg.Registry, promhttp.HandlerOpts{Registry: cfg.Registry}))

	r.Route(APIVersionPrefix, func(api chi.Router) {
		if cfg.Auth != nil {
			api.Use(cfg.Auth)
		}
		if cfg.RateLimiter != nil {
			api.Use(RateLimitMiddleware(cfg.RateLimiter))
		}
		if cfg.Routes != nil {
			cfg.Routes(api)
		}
	})

	return r
}

// applyDefaults fills in the zero values of cfg with the safe, inert
// choice for each dependency.
func (cfg *RouterConfig) applyDefaults() {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Tracer == nil {
		cfg.Tracer = noop.NewTracerProvider().Tracer("github.com/SubjectVoidLLC/the-pleiades/internal/api")
	}
	if cfg.Propagator == nil {
		cfg.Propagator = telemetry.Propagator()
	}
	if cfg.Registry == nil {
		cfg.Registry = prometheus.NewRegistry()
	}
}
