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

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/telemetry"
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
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return api.NewRouter(cfg), reg
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
		cfg.Routes = func(r chi.Router) {
			r.Get("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
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
		cfg.Routes = func(r chi.Router) {
			r.Get("/boom", func(w http.ResponseWriter, r *http.Request) {
				panic("handler exploded")
			})
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
		cfg.Routes = func(r chi.Router) {
			r.Post("/jobs/dispatch", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
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
		cfg.Routes = func(r chi.Router) {
			r.Get("/jobs", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
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
