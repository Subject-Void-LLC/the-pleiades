package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/telemetry"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// newTestRouter builds a router backed by real, isolated telemetry: a real
// SDK tracer provider (so trace IDs are valid, which a no-op tracer's are
// not), a private Prometheus registry, and a logger writing into buf.
//
// Everything here is the same machinery cmd/controller constructs. RULE 0:
// a router test that swapped any of it for a stub would prove the stub
// works, not the request pipeline.
//
// alwaysAuthenticated is the default test Auth: it stamps a fixed
// identity into every request's context without inspecting the request
// at all, unlike the real AuthMiddleware, which requires an actual Bearer
// header even when the Evaluator behind it would accept anything. Most of
// this file's tests are about tracing, metrics, and routing, not
// authentication, so they should not each have to carry a token to reach
// their own Route. TestRouter_RequireScopeEnforcesDeclaredScope below is
// the one test in this file that cares about the real thing, and it
// overrides Auth with a real evaluator via authtest.
func alwaysAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), api.IdentityKeyForTest, &auth.Identity{Subject: "test-user", Role: auth.RoleAdmin})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// allowAllRule is an AdmissionRule that permits everything, so a test
// whose subject is routing or telemetry rather than authorization gets a
// generator that never filters anything out.
type allowAllRule struct{}

func (allowAllRule) Check(_ context.Context, _ *auth.Identity, _ auth.AdmissionRequest) (auth.Effect, error) {
	return auth.EffectAllow, nil
}

// allowAllGenerator builds the real auth.NewAdmissionHATEOASGenerator over
// an always-allow chain, rather than a hand-written stub implementing the
// port. The generator is production code with real behavior (the subset
// re-intersection, the nil-identity branch), and a stub here would let a
// regression in it pass every router test in this file.
func allowAllGenerator(t testing.TB) auth.HATEOASGenerator {
	t.Helper()
	gen, err := auth.NewAdmissionHATEOASGenerator(auth.AdmissionChain{allowAllRule{}})
	if err != nil {
		t.Fatalf("building test HATEOAS generator: %v", err)
	}
	return gen
}

// Auth defaults to alwaysAuthenticated and Admission to an always-allow
// fakeAdmitter, so a registered Route reaches its handler by default. A
// test that wants to exercise real authentication or authorization
// overrides Auth/Admission in mutate.
func newTestRouter(t *testing.T, buf *bytes.Buffer, mutate func(cfg *api.RouterConfig)) (*chi.Mux, *prometheus.Registry) {
	t.Helper()

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	reg := prometheus.NewRegistry()
	cfg := api.RouterConfig{
		Logger:     slog.New(slog.NewJSONHandler(buf, nil)),
		Tracer:     tp.Tracer("test"),
		Propagator: telemetry.Propagator(),
		Registry:   reg,
		Auth:       alwaysAuthenticated,
		Admission:  &fakeAdmitter{},
		HATEOAS:    allowAllGenerator(t),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	router, err := api.NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router, reg
}

// get issues one request against router and returns the recorder.
func get(t *testing.T, router http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(method, target, nil))
	return rr
}

// TestAPIGateway_ReleaseGate is Phase 11's own Release Gate, exercised
// against the real router: a request to /healthz must emit a JSON log line
// carrying a trace_id, and /metrics must then report exactly one request
// for that route.
//
// The gate's original wording asked only for a log field named trace_id,
// which a middleware that minted a UUID satisfied without any tracing
// existing. This version additionally requires that the trace_id in the
// log line is the trace ID of the real OpenTelemetry span that served the
// request, which is the requirement the phase title actually asserts.
func TestAPIGateway_ReleaseGate(t *testing.T) {
	var logBuf bytes.Buffer
	router, reg := newTestRouter(t, &logBuf, nil)

	rr := get(t, router, http.MethodGet, "/healthz")

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /healthz: got status %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /healthz body: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("GET /healthz: got status field %v, want \"ok\"", body["status"])
	}

	traceIDHeader := rr.Header().Get("X-Trace-ID")
	if traceIDHeader == "" {
		t.Fatal("GET /healthz: no X-Trace-ID response header")
	}

	logLine := decodeLogLine(t, logBuf.Bytes())
	if logLine["trace_id"] != traceIDHeader {
		t.Errorf("log line trace_id is %v, want the served span's trace ID %q", logLine["trace_id"], traceIDHeader)
	}
	if logLine["route"] != "/healthz" {
		t.Errorf("log line route is %v, want %q", logLine["route"], "/healthz")
	}
	if logLine["status"] != float64(http.StatusOK) {
		t.Errorf("log line status is %v, want %d", logLine["status"], http.StatusOK)
	}

	if got := counterValue(t, reg, "http_requests_total", map[string]string{
		"route": "/healthz", "method": http.MethodGet, "code": "200",
	}); got != 1 {
		t.Errorf("http_requests_total for /healthz is %v, want 1", got)
	}

	metrics := get(t, router, http.MethodGet, "/metrics")
	for _, want := range []string{"http_requests_total", "http_request_duration_seconds", "http_requests_in_flight"} {
		if !strings.Contains(metrics.Body.String(), want) {
			t.Errorf("/metrics does not expose %q", want)
		}
	}
}

// TestRouter_MetricLabelUsesRoutePatternNotPath proves the cardinality
// bound: two requests to the same route with different URL parameters must
// land on one label set, and an unmatched path must not mint a new one at
// all. Without this, anyone who can reach the port can grow the metric's
// label set without limit simply by requesting new 404s.
func TestRouter_MetricLabelUsesRoutePatternNotPath(t *testing.T) {
	var logBuf bytes.Buffer
	router, reg := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Routes = []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs/{id}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}},
		}
	})

	get(t, router, http.MethodGet, "/api/v1/jobs/aaaaaaaa")
	get(t, router, http.MethodGet, "/api/v1/jobs/bbbbbbbb")
	get(t, router, http.MethodGet, "/nope/one")
	get(t, router, http.MethodGet, "/nope/two")

	if got := counterValue(t, reg, "http_requests_total", map[string]string{
		"route": "/api/v1/jobs/{id}", "method": http.MethodGet, "code": "200",
	}); got != 2 {
		t.Errorf("both parameterized requests should share one label set: got %v, want 2", got)
	}
	if got := counterValue(t, reg, "http_requests_total", map[string]string{
		"route": "unmatched", "method": http.MethodGet, "code": "404",
	}); got != 2 {
		t.Errorf("both unmatched requests should share the unmatched label: got %v, want 2", got)
	}
	for _, label := range labelValues(t, reg, "http_requests_total", "route") {
		if strings.Contains(label, "aaaaaaaa") || strings.Contains(label, "nope") {
			t.Errorf("raw URL path leaked into the route label: %q", label)
		}
	}
}

// TestRouter_ContinuesUpstreamTrace proves the propagator is really wired:
// a request arriving with a W3C traceparent header must be served by a
// span in that same trace, not a new one. This is the ingress half of
// PLAN.md Section 19's span propagation requirement.
func TestRouter_ContinuesUpstreamTrace(t *testing.T) {
	var logBuf bytes.Buffer
	router, _ := newTestRouter(t, &logBuf, nil)

	const upstreamTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("traceparent", "00-"+upstreamTraceID+"-00f067aa0ba902b7-01")

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Trace-ID"); got != upstreamTraceID {
		t.Errorf("served span is in trace %q, want the caller's trace %q", got, upstreamTraceID)
	}
}

// TestRouter_RecovererTurnsPanicIntoObserved500 proves the Recoverer is
// mounted and, just as importantly, that it is mounted *inside* the
// telemetry middleware: a panicking handler must produce a 500 that the
// metrics and the log line both record. A Recoverer mounted outermost
// would still return 500 while making the request invisible to both.
func TestRouter_RecovererTurnsPanicIntoObserved500(t *testing.T) {
	var logBuf bytes.Buffer
	router, reg := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Routes = []api.Route{
			{Method: http.MethodGet, Pattern: "/boom", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, r *http.Request) {
				panic("handler exploded")
			}},
		}
	})

	rr := get(t, router, http.MethodGet, "/api/v1/boom")

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("panicking handler returned %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	if got := counterValue(t, reg, "http_requests_total", map[string]string{
		"route": "/api/v1/boom", "method": http.MethodGet, "code": "500",
	}); got != 1 {
		t.Errorf("panic was not counted as a 500: got %v, want 1", got)
	}
	if got := gaugeValue(t, reg, "http_requests_in_flight"); got != 0 {
		t.Errorf("in-flight gauge leaked after a panic: got %v, want 0", got)
	}
	if line := decodeLogLine(t, logBuf.Bytes()); line["status"] != float64(http.StatusInternalServerError) {
		t.Errorf("panic was logged with status %v, want 500", line["status"])
	}
}

// TestRouter_ReadyzReflectsDependencies proves /readyz is dependency
// aware and /healthz deliberately is not. A liveness probe that fails on a
// broken dependency tells the orchestrator to restart a process a restart
// cannot fix.
func TestRouter_ReadyzReflectsDependencies(t *testing.T) {
	natsUp := true
	var logBuf bytes.Buffer
	router, _ := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		// This test flips a dependency and asks again immediately, which
		// is an assertion of ZERO staleness. /readyz no longer offers that
		// to anybody: its answers are bounded by a minimum interval so an
		// unauthenticated caller cannot drive unbounded database work
		// (internal/api's readinessGate). One nanosecond keeps this test
		// about what it was always about, whether the endpoint reflects a
		// broken dependency, and leaves the bound itself to the tests
		// written for it in readiness_test.go.
		cfg.ReadinessMinInterval = time.Nanosecond
		cfg.Readiness = []api.ReadinessCheck{
			{Name: "nats", Probe: func(ctx context.Context) error {
				if !natsUp {
					return errors.New("nats connection is DISCONNECTED at nats://10.0.0.5:4222")
				}
				return nil
			}},
			{Name: "database", Probe: func(ctx context.Context) error { return nil }},
		}
	})

	rr := get(t, router, http.MethodGet, "/readyz")
	if rr.Code != http.StatusOK {
		t.Errorf("healthy /readyz returned %d, want %d", rr.Code, http.StatusOK)
	}

	natsUp = false

	rr = get(t, router, http.MethodGet, "/readyz")
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz with NATS down returned %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}
	var body struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decoding /readyz body: %v", err)
	}
	if body.Checks["nats"] != "failed" || body.Checks["database"] != "ok" {
		t.Errorf("/readyz reported %v, want nats failed and database ok", body.Checks)
	}
	// The unauthenticated body must not echo the probe's error text: it
	// names an internal broker address.
	if strings.Contains(rr.Body.String(), "10.0.0.5") {
		t.Error("/readyz leaked the dependency's internal address into an unauthenticated response body")
	}

	if live := get(t, router, http.MethodGet, "/healthz"); live.Code != http.StatusOK {
		t.Errorf("/healthz returned %d while a dependency was down, want %d: liveness must not track dependencies",
			live.Code, http.StatusOK)
	}
}

// TestRouter_EveryApplicationRouteIsVersioned proves the structural
// guarantee RouterConfig.Routes exists for: a handler registered through
// it is reachable only under /api/v1, never at the bare path, so an
// unversioned route cannot be added by accident.
func TestRouter_EveryApplicationRouteIsVersioned(t *testing.T) {
	var logBuf bytes.Buffer
	router, _ := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Routes = []api.Route{
			{Method: http.MethodPost, Pattern: "/jobs/dispatch", Scope: auth.ScopeRunbookExecute, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			}},
		}
	})

	if rr := get(t, router, http.MethodPost, api.APIVersionPrefix+"/jobs/dispatch"); rr.Code != http.StatusAccepted {
		t.Errorf("versioned route returned %d, want %d", rr.Code, http.StatusAccepted)
	}
	if rr := get(t, router, http.MethodPost, "/jobs/dispatch"); rr.Code != http.StatusNotFound {
		t.Errorf("unversioned path returned %d, want %d", rr.Code, http.StatusNotFound)
	}
}

// TestRouter_OperationalEndpointsBypassAuthAndRateLimit proves the
// deliberate exception documented on APIVersionPrefix: probes and the
// scrape endpoint are reachable without a token and are never throttled.
// Rate limiting a liveness probe is how a busy replica gets restarted for
// being busy.
func TestRouter_OperationalEndpointsBypassAuthAndRateLimit(t *testing.T) {
	var logBuf bytes.Buffer
	router, _ := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Auth = func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
			})
		}
		cfg.RateLimiter = api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 1, Burst: 1})
		cfg.Routes = []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }},
		}
	})

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		for i := 0; i < 5; i++ {
			if rr := get(t, router, http.MethodGet, path); rr.Code != http.StatusOK {
				t.Fatalf("%s request %d returned %d, want %d", path, i, rr.Code, http.StatusOK)
			}
		}
	}
	if rr := get(t, router, http.MethodGet, api.APIVersionPrefix+"/jobs"); rr.Code != http.StatusUnauthorized {
		t.Errorf("versioned route without a token returned %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

// TestRouter_RequireScopeEnforcesDeclaredScope is Phase 12's own Release
// Gate, exercised end to end against the real router, the real
// auth.Admission Chain of Responsibility, and a real signed token (RULE
// 0: a test that stubbed out Auth or Admission here would prove nothing
// about whether the two actually compose). It replaces
// api.IdentityKeyForTest with authtest.Issuer: every identity below
// reaches the handler, or does not, purely because a real token really
// validated and really carried (or lacked) the declared scope, not
// because anything was injected around the check.
//
// Three identities, one request path, three outcomes: no token is 401
// (authentication never happened), the wrong scope is 403 (authenticated,
// not admitted), the right scope is 200. Before Phase 12 the second case
// did not exist: AuthMiddleware alone cannot distinguish "wrong scope"
// from "right scope," and nothing before this file called
// auth.AdmissionChain.Evaluate outside of internal/auth's own tests.
func TestRouter_RequireScopeEnforcesDeclaredScope(t *testing.T) {
	issuer := authtest.New(t, "router-test-issuer", "router-test-audience")
	admission := auth.Admission{Chain: auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}}

	var logBuf bytes.Buffer
	router, _ := newTestRouter(t, &logBuf, func(cfg *api.RouterConfig) {
		cfg.Auth = api.AuthMiddleware(issuer.Evaluator())
		cfg.Admission = admission
		cfg.Routes = []api.Route{
			{Method: http.MethodPost, Pattern: "/jobs/dispatch", Scope: auth.ScopeRunbookExecute, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}},
		}
	})

	dispatch := func(t *testing.T, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/jobs/dispatch", nil)
		if bearer != "" {
			req.Header.Set("Authorization", bearer)
		}
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	if rr := dispatch(t, ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	wrongScope := &auth.Identity{Subject: "viewer1", Role: auth.RoleViewer, Scopes: []auth.Scope{auth.ScopeInventoryRead}}
	if rr := dispatch(t, issuer.BearerToken(t, wrongScope)); rr.Code != http.StatusForbidden {
		t.Errorf("wrong scope: got %d, want %d", rr.Code, http.StatusForbidden)
	}

	rightScope := &auth.Identity{Subject: "operator1", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeRunbookExecute}}
	if rr := dispatch(t, issuer.BearerToken(t, rightScope)); rr.Code != http.StatusOK {
		t.Errorf("right scope: got %d, want %d", rr.Code, http.StatusOK)
	}
}

// --- helpers ---

// decodeLogLine decodes the last complete JSON log line in raw.
func decodeLogLine(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no log line was emitted")
	}
	var line map[string]interface{}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &line); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, lines[len(lines)-1])
	}
	return line
}

// counterValue gathers reg and returns the value of the counter named
// name whose labels exactly match want, or 0 if there is no such series.
func counterValue(t *testing.T, reg *prometheus.Registry, name string, want map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			got := make(map[string]string, len(metric.GetLabel()))
			for _, label := range metric.GetLabel() {
				got[label.GetName()] = label.GetValue()
			}
			if fmt.Sprint(got) == fmt.Sprint(want) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// gaugeValue gathers reg and returns the value of the unlabeled gauge
// named name.
func gaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			return metric.GetGauge().GetValue()
		}
	}
	return 0
}

// labelValues returns every distinct value the label named label takes on
// the metric family named name.
func labelValues(t *testing.T, reg *prometheus.Registry, name, label string) []string {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	var values []string
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, l := range metric.GetLabel() {
				if l.GetName() == label {
					values = append(values, l.GetValue())
				}
			}
		}
	}
	return values
}
