package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// APIVersionPrefix is the one versioned surface every application route
// lives under, and the reason no per-client gateway is needed.
//
// The operational endpoints below are deliberately not under it. /healthz,
// /readyz, and /metrics are contracts with the orchestrator and the
// scrape agent, not with an API consumer: a Kubernetes probe path and a
// Prometheus scrape path are conventional, and versioning them would break
// every stock chart and scrape config for no benefit, since they carry no
// payload schema that could ever need a v2.
const APIVersionPrefix = "/api/v1"

// Route is one entry in the application's route table. Scope is not
// optional: it is what turns "every route is authorized" from a rule a
// handler author has to remember into a property NewRouter can refuse to
// build without. There is no way to register a Route with an empty Scope
// and have the router come up; see validateRoutes.
type Route struct {
	// Method is the HTTP method this Route answers, e.g. http.MethodGet.
	Method string

	// Pattern is the chi route pattern, relative to APIVersionPrefix (e.g.
	// "/jobs/{id}/logs", not "/api/v1/jobs/{id}/logs").
	Pattern string

	// Scope is the auth.Scope a caller must be admitted for. Enforced by
	// api.RequireScope, mounted per route rather than once for the whole
	// subtree, since two routes can require two different scopes.
	Scope auth.Scope

	// Rel is the hypermedia relation name this route appears under in a
	// response's _links array, and, with Scope, is what the OPTIONS
	// handler filters the Allow header by.
	//
	// It is not optional, for the same reason Scope is not: a route with
	// no declared relation is a route the API cannot describe to a
	// client, and leaving it to a handler author to remember is how half
	// a route table ends up undiscoverable. validateRoutes refuses to
	// build a router without it.
	//
	// It is declared here rather than derived from Method because a
	// derivation cannot tell two POSTs on one resource apart
	// ("/jobs/{id}/cancel" versus "/jobs/{id}/retry"), which is exactly
	// the case hypermedia exists for, and because it would silently
	// rename the relation every client keys on the day a route moved
	// from PUT to PATCH.
	Rel auth.LinkRel

	// Handler is the application handler. It runs only after tracing,
	// metrics, logging, auth, the rate limiter, and RequireScope have all
	// already accepted the request.
	Handler http.HandlerFunc
}

// RouterConfig is the full set of dependencies the Front Controller needs.
// Every field is optional except where its own doc comment says
// otherwise: NewRouter substitutes a safe default for any other zero
// value, so a test can ask for the piece it is testing and ignore the
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

	// Auth guards the versioned API subtree and is what places an
	// *auth.Identity in context for RequireScope and every handler to
	// read. Nil is a construction error unless AllowUnauthenticated is
	// also set: an unauthenticated API subtree is only ever right in a
	// test, and Phase 12's own finding is that leaving this reachable by
	// omission is exactly how a real deployment ends up with one by
	// accident. It is applied before RateLimiter so the limiter can key on
	// the resolved identity rather than on a source address, and before
	// each Route's own RequireScope, which depends on the identity Auth
	// places in context.
	Auth func(http.Handler) http.Handler

	// AllowUnauthenticated is the explicit, named opt-out for a nil Auth.
	// It exists so "no authentication" is a choice a caller states, not a
	// default NewRouter falls into when a field is left zero.
	AllowUnauthenticated bool

	// Admission enforces the auth.Scope each Route declares, via
	// RequireScope. Required whenever Routes is non-empty: a route table
	// that declares scopes with nothing able to check them would be
	// exactly the same silent gap Phase 12 closed, just moved one layer
	// down. auth.Admission satisfies this.
	Admission Admitter

	// Routes registers the application's own handlers. Every entry is
	// mounted under APIVersionPrefix, behind Auth, RateLimiter, and its
	// own RequireScope(Admission, Route.Scope), which is what makes "every
	// route is versioned, authenticated, throttled, and authorized" a
	// structural property rather than a rule each new route must
	// remember.
	Routes []Route

	// HATEOAS decides which of a resource's declared affordances a given
	// caller may actually exercise, for both the _links array Respond
	// emits and the Allow header OPTIONS returns. Required whenever
	// Routes is non-empty.
	//
	// It is required rather than optional deliberately. The alternative,
	// "a nil generator degrades to a self link only," is exactly what the
	// middleware this phase deleted did, and it is the vacuous pass
	// Phase 13's Adversarial gate is written against: a route table that
	// declares relations with nothing able to resolve them is the same
	// silent gap Phase 12 closed for Scope, moved one layer down.
	//
	// Build it from the same auth.AdmissionChain value Admission wraps.
	// That is what makes a link and a 403 unable to disagree: they are
	// one evaluation against one rule list, not two implementations of
	// the same question. See auth.NewAdmissionHATEOASGenerator.
	HATEOAS auth.HATEOASGenerator
}

// NewRouter builds the Front Controller: one chi.Mux that owns routing and
// every cross-cutting concern. It returns an error, rather than panicking
// or silently building an open router, for every configuration this
// package considers unsafe to serve: see validateConfig. This mirrors
// NewJWTEvaluator and NewStaticKeyProvider, which already fail closed at
// construction rather than at the first request.
//
// The middleware order matters and is not arbitrary. Tracing is outermost
// so every later middleware runs inside the request span and every log
// line can carry its trace ID. Metrics is next so it observes the status
// code the Recoverer writes for a panic, rather than never running at all.
// The logger is next for the same reason. Recoverer is innermost of the
// four, so a panic in a handler becomes a 500 that tracing, metrics, and
// logging all see, instead of a torn connection that none of them record.
// Inside APIVersionPrefix: Auth, then RateLimiter (so it can key on the
// resolved identity), then each Route's own RequireScope, then the
// handler.
func NewRouter(cfg RouterConfig) (*chi.Mux, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	r.Use(TracingMiddleware(cfg.Tracer, cfg.Propagator))
	r.Use(MetricsMiddleware(NewMetrics(cfg.Registry)))
	r.Use(StructuredLoggerMiddleware(cfg.Logger))
	r.Use(middleware.Recoverer)

	r.Get("/healthz", healthzHandler)
	r.Get("/readyz", readyzHandler(cfg.Logger, cfg.Readiness))
	r.Handle("/metrics", promhttp.HandlerFor(cfg.Registry, promhttp.HandlerOpts{Registry: cfg.Registry}))
	registerWellKnown(r)
	registerOpenAPI(r)

	// One builder, built once, shared by every request. It is the single
	// source of truth behind both the _links array Respond emits and the
	// Allow header OPTIONS returns, which is what makes the two unable to
	// disagree.
	builder := newLinkBuilder(cfg.Routes, cfg.HATEOAS)

	r.Route(APIVersionPrefix, func(api chi.Router) {
		if cfg.Auth != nil {
			api.Use(cfg.Auth)
		}
		if cfg.RateLimiter != nil {
			api.Use(RateLimitMiddleware(cfg.RateLimiter))
		}
		// After Auth, so the builder's own consumers can read the
		// identity Auth resolved; before the routes, so every handler
		// under this subtree can call Respond.
		api.Use(withLinkBuilder(builder))

		for _, route := range cfg.Routes {
			api.With(RequireScope(cfg.Admission, route.Scope)).Method(route.Method, route.Pattern, route.Handler)
		}

		// Registered after the application routes so an OPTIONS handler
		// never shadows one, and guarded by validateRoutes, which refuses
		// a Route that claims the OPTIONS method for itself.
		registerOptions(api, builder, cfg.Routes)
	})

	return r, nil
}

// applyDefaults fills in the zero values of cfg with the safe, inert
// choice for each dependency that has one. Auth and Admission have no
// safe default and are left for validate to reject instead.
func (cfg *RouterConfig) applyDefaults() {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Tracer == nil {
		cfg.Tracer = noop.NewTracerProvider().Tracer("github.com/Subject-Void-LLC/the-pleiades/internal/api")
	}
	if cfg.Propagator == nil {
		cfg.Propagator = telemetry.Propagator()
	}
	if cfg.Registry == nil {
		cfg.Registry = prometheus.NewRegistry()
	}
}

// validate rejects every RouterConfig shape this package considers unsafe
// to serve. Called after applyDefaults, so it only ever sees the two
// fields that have no safe default: Auth/AllowUnauthenticated and
// Admission/Routes.
func (cfg *RouterConfig) validate() error {
	if cfg.Auth == nil && !cfg.AllowUnauthenticated {
		return fmt.Errorf("api: RouterConfig.Auth is nil; set AllowUnauthenticated to serve the API subtree unauthenticated")
	}
	if len(cfg.Routes) > 0 && cfg.Admission == nil {
		return fmt.Errorf("api: RouterConfig.Routes is non-empty but Admission is nil, so no declared Route.Scope could ever be enforced")
	}
	if len(cfg.Routes) > 0 && cfg.HATEOAS == nil {
		return fmt.Errorf("api: RouterConfig.Routes is non-empty but HATEOAS is nil, so every response would advertise no affordances and every Allow header would be empty")
	}
	return validateRoutes(cfg.Routes)
}

// validateRoutes rejects every Route table shape this package cannot serve
// honestly:
//
//   - an empty Scope, an authorization gap indistinguishable from an
//     oversight;
//   - an empty Rel, a route no response can describe and no Allow header
//     can name;
//   - two Routes on the same Method and Pattern, which chi would let
//     silently shadow one another, so a route table serves the wrong
//     handler under its own name;
//   - two Routes sharing a Rel on one Pattern, which would put two
//     indistinguishable entries in one _links array;
//   - a Route claiming OPTIONS, which the router owns on every pattern
//     (see registerOptions) and which a handler-supplied one would shadow;
//   - a wildcard Pattern, which has no stable href to build a link from.
func validateRoutes(routes []Route) error {
	seenRoute := make(map[string]bool, len(routes))
	seenRel := make(map[string]bool, len(routes))
	for _, route := range routes {
		if route.Scope == "" {
			return fmt.Errorf("api: Route %s %s declares no Scope", route.Method, route.Pattern)
		}
		if route.Rel == "" {
			return fmt.Errorf("api: Route %s %s declares no Rel, so no response could describe it and no Allow header could name it", route.Method, route.Pattern)
		}
		if route.Method == http.MethodOptions {
			return fmt.Errorf("api: Route %s %s declares the OPTIONS method, which the router owns on every pattern", route.Method, route.Pattern)
		}
		if strings.Contains(route.Pattern, "*") {
			return fmt.Errorf("api: Route %s %s uses a wildcard pattern, which has no stable href to build a link from", route.Method, route.Pattern)
		}
		key := route.Method + " " + route.Pattern
		if seenRoute[key] {
			return fmt.Errorf("api: Route %s is registered more than once", key)
		}
		seenRoute[key] = true

		relKey := route.Pattern + " " + string(route.Rel)
		if seenRel[relKey] {
			return fmt.Errorf("api: Route pattern %s declares the relation %q more than once, so its _links array would carry two entries a client cannot tell apart", route.Pattern, route.Rel)
		}
		seenRel[relKey] = true
	}
	return nil
}
