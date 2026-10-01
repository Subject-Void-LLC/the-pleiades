package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/prometheus/client_golang/prometheus"
)

// alwaysAllowAdmitter is a minimal Admitter that never denies, since this
// fuzzer's target is the tracing/propagator boundary (an attacker-supplied
// traceparent), not authorization. AllowUnauthenticated stays false and a
// real, if trivial, evaluator is wired below so the request still passes
// through AuthMiddleware, matching the shape of a real deployment rather
// than skipping a pipeline stage the real router always runs.
type alwaysAllowAdmitter struct{}

func (alwaysAllowAdmitter) Evaluate(context.Context, *auth.Identity, auth.AdmissionRequest) error {
	return nil
}

// alwaysValidEvaluator accepts any bearer value as an authenticated
// identity, isolating this fuzzer from auth.jwtEvaluator's own parsing (a
// distinct boundary, fuzzed directly by internal/auth's own
// FuzzJWTParsing).
type alwaysValidEvaluator struct{}

func (alwaysValidEvaluator) ValidateToken(context.Context, string) (*auth.Identity, error) {
	return &auth.Identity{Subject: "fuzz-user"}, nil
}

// FuzzAPIRouter drives arbitrary methods, paths, traceparent headers, and
// Authorization headers through the whole middleware chain. The
// traceparent input matters: propagator.Extract parses attacker-supplied
// hex, and a malformed value must produce an ordinary un-parented span
// rather than a panic that takes the process down. The Authorization
// input matters for the same reason AuthMiddleware.ValidateToken's own
// prefix-slicing exists: a header shorter than "Bearer " must not panic
// on the slice bound.
func FuzzAPIRouter(f *testing.F) {
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Registry:  prometheus.NewRegistry(),
		Auth:      api.AuthMiddleware(alwaysValidEvaluator{}),
		Admission: alwaysAllowAdmitter{},
		HATEOAS:   allowAllGenerator(f),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs/{id}", Scope: auth.ScopeJobRead, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}},
		},
	})
	if err != nil {
		f.Fatalf("NewRouter: %v", err)
	}

	f.Add("GET", "/healthz", "", "")
	f.Add("GET", "/readyz", "", "")
	f.Add("POST", "/metrics", "", "")
	f.Add("PUT", "/unknown", "", "")
	f.Add("GET", "/../../etc/passwd", "", "")
	f.Add("GET", "/api/v1/jobs/abc", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "Bearer x")
	f.Add("GET", "/api/v1/jobs/abc", "not-a-traceparent", "Bearer x")
	f.Add("GET", "/api/v1/jobs/abc", "00--00f067aa0ba902b7-01", "")
	f.Add("GET", "/healthz", "99-ffffffffffffffffffffffffffffffff-ffffffffffffffff-ff", "")
	f.Add("GET", "/api/v1/jobs/abc", "", "Bearer")
	f.Add("GET", "/api/v1/jobs/abc", "", "B")

	f.Fuzz(func(t *testing.T, method, path, traceparent, authorization string) {
		// http.NewRequest is used here instead of httptest.NewRequest
		// deliberately: httptest.NewRequest panics on a method or URI the
		// fuzzer invents that net/http cannot parse at all, which used to
		// require a blanket recover() to survive. IMPLEMENTATION.md flags
		// exactly that shape as structurally preventing a fuzz target from
		// reporting the panics it exists to find (internal/api/
		// hateoas_fuzz_test.go's own comment documents the identical
		// house fix). http.NewRequest reports the same condition as an
		// ordinary error instead of panicking, so it can be skipped like
		// any other input this test harness itself cannot express, rather
		// than absorbed by a recover() that would also swallow a genuine
		// panic inside the router.
		req, err := http.NewRequest(method, path, nil)
		if err != nil {
			t.Skip("http.NewRequest cannot express this method and path as a request")
		}
		if traceparent != "" {
			req.Header.Set("traceparent", traceparent)
		}
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		rr := httptest.NewRecorder()

		router.ServeHTTP(rr, req)
	})
}
