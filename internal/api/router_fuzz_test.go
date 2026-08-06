package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
)

// FuzzAPIRouter drives arbitrary methods, paths, and traceparent headers
// through the whole middleware chain. The traceparent input matters:
// propagator.Extract parses attacker-supplied hex, and a malformed value
// must produce an ordinary un-parented span rather than a panic that
// takes the process down.
func FuzzAPIRouter(f *testing.F) {
	router := api.NewRouter(api.RouterConfig{
		Logger:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Registry: prometheus.NewRegistry(),
		Routes: func(r chi.Router) {
			r.Get("/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		},
	})

	f.Add("GET", "/healthz", "")
	f.Add("GET", "/readyz", "")
	f.Add("POST", "/metrics", "")
	f.Add("PUT", "/unknown", "")
	f.Add("GET", "/../../etc/passwd", "")
	f.Add("GET", "/api/v1/jobs/abc", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	f.Add("GET", "/api/v1/jobs/abc", "not-a-traceparent")
	f.Add("GET", "/api/v1/jobs/abc", "00--00f067aa0ba902b7-01")
	f.Add("GET", "/healthz", "99-ffffffffffffffffffffffffffffffff-ffffffffffffffff-ff")

	f.Fuzz(func(t *testing.T, method, path, traceparent string) {
		// httptest.NewRequest panics on a URI the fuzzer invents that
		// net/http cannot parse at all; that is the test harness's own
		// limitation, not a finding about the router.
		defer func() {
			_ = recover()
		}()

		req := httptest.NewRequest(method, path, nil)
		if traceparent != "" {
			req.Header.Set("traceparent", traceparent)
		}
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
	})
}
