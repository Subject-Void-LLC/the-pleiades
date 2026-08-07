package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// BenchmarkRequireScope measures RequireScope's own overhead in
// isolation: an already-authenticated request, an admitter that always
// allows. This is the cost Phase 12 adds to every request that reaches a
// registered Route, on top of AuthMiddleware's own pre-existing cost
// (BenchmarkAuthMiddleware, middleware_bench_test.go).
func BenchmarkRequireScope(b *testing.B) {
	handler := api.RequireScope(&fakeAdmitter{}, auth.ScopeRunbookExecute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/jobs/dispatch", nil)
	req = req.WithContext(contextWithIdentity(req, &auth.Identity{Subject: "bench-user", Role: auth.RoleOperator}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}

// BenchmarkAPIMiddleware_SecuredRoute measures the full per-request cost
// of a Route that is versioned, authenticated, and authorized end to end:
// the shape every real application route now has (cmd/controller's own
// dispatcher and log-streamer routes), compared against
// BenchmarkAPIMiddleware (router_bench_test.go), the identical tracing/
// metrics/logging chain with no application route mounted at all. The
// difference between the two numbers is the honest price of "every route
// enforces its own scope," not a number in isolation. Logger and Tracer
// are set to the same discard/no-op choices BenchmarkAPIMiddleware uses,
// so neither benchmark's number includes real stdout I/O or a real
// exporter; only the middleware chain itself differs between them.
func BenchmarkAPIMiddleware_SecuredRoute(b *testing.B) {
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(b),
		Routes: []api.Route{
			{Method: http.MethodPost, Pattern: "/jobs/dispatch", Scope: auth.ScopeRunbookExecute, Rel: auth.RelSelf, Handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}},
		},
	})
	if err != nil {
		b.Fatalf("NewRouter: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/jobs/dispatch", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
	}
}
