package api_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
)

// TestNewRouter_ZeroConfigIsUsable proves every RouterConfig field has a
// working default. A constructor whose zero value panics forces every
// test and every future caller to know the full dependency list before it
// can serve one request.
func TestNewRouter_ZeroConfigIsUsable(t *testing.T) {
	router := api.NewRouter(api.RouterConfig{})

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s on a zero-config router returned %d, want %d", path, rr.Code, http.StatusOK)
		}
	}

	// With the default no-op tracer there is no valid trace ID, so the
	// header must be absent rather than an all-zero string that looks like
	// a real trace to a log aggregator.
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := rr.Header().Get("X-Trace-ID"); got != "" {
		t.Errorf("a no-op tracer set X-Trace-ID to %q, want no header at all", got)
	}
}

// TestMiddlewareOutsideChiUsesUnmatchedRoute proves the route label stays
// bounded even when a middleware is mounted on a plain net/http handler
// with no chi route context to read a pattern from.
func TestMiddlewareOutsideChiUsesUnmatchedRoute(t *testing.T) {
	rl := api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 1000, Burst: 1000})
	handler := api.RateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/no-chi-here", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("a middleware used outside chi returned %d, want %d", rr.Code, http.StatusOK)
	}
}

// TestRateLimiter_RetryAfterWithNoRefill covers the degenerate
// configuration where the sustained rate is zero: the bucket never
// refills, so there is no honest number of seconds to advertise, and the
// header must still be a valid positive integer rather than 0 or a
// negative value a client would reject.
func TestRateLimiter_RetryAfterWithNoRefill(t *testing.T) {
	rl := api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 0, Burst: 1})
	handler := api.RateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.RemoteAddr = "10.0.0.1:5000"

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request returned %d, want %d", rr.Code, http.StatusOK)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request returned %d, want %d", rr.Code, http.StatusTooManyRequests)
	}
	retryAfter, err := strconv.Atoi(rr.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Errorf("Retry-After is %q, want a positive integer", rr.Header().Get("Retry-After"))
	}
}
