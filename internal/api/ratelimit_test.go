package api_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// TestRateLimiter_Allow is the table-driven core: a bucket admits its
// burst back to back and then refuses, and two distinct callers never
// share one budget.
func TestRateLimiter_Allow(t *testing.T) {
	tests := []struct {
		name    string
		cfg     api.RateLimiterConfig
		keys    []string
		allowed []bool
	}{
		{
			name:    "burst is admitted then exhausted",
			cfg:     api.RateLimiterConfig{RequestsPerSecond: 0.001, Burst: 3},
			keys:    []string{"a", "a", "a", "a"},
			allowed: []bool{true, true, true, false},
		},
		{
			name:    "each caller gets its own bucket",
			cfg:     api.RateLimiterConfig{RequestsPerSecond: 0.001, Burst: 1},
			keys:    []string{"a", "b", "c", "a"},
			allowed: []bool{true, true, true, false},
		},
		{
			name:    "zero burst is treated as one, never as zero",
			cfg:     api.RateLimiterConfig{RequestsPerSecond: 0.001, Burst: 0},
			keys:    []string{"a", "a"},
			allowed: []bool{true, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := api.NewRateLimiter(tt.cfg)
			for i, key := range tt.keys {
				if got := rl.Allow(key); got != tt.allowed[i] {
					t.Errorf("request %d for key %q: Allow=%v, want %v", i, key, got, tt.allowed[i])
				}
			}
		})
	}
}

// TestRateLimiter_CallerTableIsBounded proves the limiter cannot itself
// become the memory exhaustion vector it exists to prevent. The caller key
// for an unauthenticated request is its source address, which an attacker
// varies freely, so an unbounded table would let the defense kill the
// process.
//
// It asserts on behavior rather than on the internal map: after admitting
// far more distinct callers than the cap, an old caller's bucket must have
// been evicted, which shows as that caller getting a fresh full burst.
func TestRateLimiter_CallerTableIsBounded(t *testing.T) {
	const maxCallers = 16
	rl := api.NewRateLimiter(api.RateLimiterConfig{
		RequestsPerSecond: 0.001,
		Burst:             1,
		MaxCallers:        maxCallers,
	})

	if !rl.Allow("victim") {
		t.Fatal("first request from victim was refused")
	}
	if rl.Allow("victim") {
		t.Fatal("second request from victim was admitted: its bucket should be empty")
	}

	for i := 0; i < maxCallers*4; i++ {
		rl.Allow("flood-" + strconv.Itoa(i))
	}

	// victim was the least recently seen entry long before the flood
	// finished, so its bucket must have been evicted rather than the table
	// growing without limit.
	if !rl.Allow("victim") {
		t.Error("victim's bucket survived a flood of 64 distinct callers past a cap of 16: the table is not bounded")
	}
}

// TestRateLimitMiddleware_KeyingAndResponse proves the two things the
// middleware must get right: an authenticated request is billed to its
// identity rather than its source address, and a refused request answers
// 429 with a usable Retry-After.
func TestRateLimitMiddleware_KeyingAndResponse(t *testing.T) {
	rl := api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 0.001, Burst: 1})
	handler := api.RateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Two requests from one identity but two different source addresses:
	// keying on the identity means the second is refused.
	first := identityRequest(t, "ci-pipeline", "10.0.0.1:5000")
	second := identityRequest(t, "ci-pipeline", "10.0.0.2:5000")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, first)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request returned %d, want %d", rr.Code, http.StatusOK)
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, second)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("second request from the same identity returned %d, want %d: the limiter keyed on the address, not the identity",
			rr.Code, http.StatusTooManyRequests)
	}
	retryAfter, err := strconv.Atoi(rr.Header().Get("Retry-After"))
	if err != nil || retryAfter < 1 {
		t.Errorf("Retry-After is %q, want a positive integer number of seconds", rr.Header().Get("Retry-After"))
	}

	// A different identity is unaffected by the first one's exhausted
	// bucket.
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, identityRequest(t, "operator", "10.0.0.1:5000"))
	if rr.Code != http.StatusOK {
		t.Errorf("a second identity returned %d, want %d: one caller spent another's budget", rr.Code, http.StatusOK)
	}
}

// TestRateLimitMiddleware_IgnoresForwardedForHeaders proves the limiter
// cannot be bypassed by a caller varying an X-Forwarded-For header.
// Trusting that header would mint a fresh, full bucket per request, which
// is worse than no limiter because it looks like protection.
func TestRateLimitMiddleware_IgnoresForwardedForHeaders(t *testing.T) {
	rl := api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 0.001, Burst: 1})
	handler := api.RateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	statuses := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
		req.RemoteAddr = "10.0.0.1:5000"
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i))
		req.Header.Set("X-Real-IP", "198.51.100."+strconv.Itoa(i))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		statuses = append(statuses, rr.Code)
	}

	if statuses[0] != http.StatusOK {
		t.Fatalf("first request returned %d, want %d", statuses[0], http.StatusOK)
	}
	for i := 1; i < len(statuses); i++ {
		if statuses[i] != http.StatusTooManyRequests {
			t.Errorf("request %d returned %d, want %d: a spoofed forwarding header bypassed the limiter",
				i, statuses[i], http.StatusTooManyRequests)
		}
	}
}

// TestRateLimitMiddleware_UnparseableRemoteAddrKeepsDistinctCallers proves
// a RemoteAddr with no port (a Unix socket, or a synthesized request) is
// used whole rather than discarded, so such callers are not silently
// collapsed onto one shared bucket.
func TestRateLimitMiddleware_UnparseableRemoteAddrKeepsDistinctCallers(t *testing.T) {
	rl := api.NewRateLimiter(api.RateLimiterConfig{RequestsPerSecond: 0.001, Burst: 1})
	handler := api.RateLimitMiddleware(rl)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, addr := range []string{"@unix-socket-a", "@unix-socket-b"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
		req.RemoteAddr = addr
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("caller %q returned %d, want %d", addr, rr.Code, http.StatusOK)
		}
	}
}

// identityRequest builds a request already carrying an authenticated
// identity, as AuthMiddleware would leave it.
func identityRequest(t *testing.T, subject, remoteAddr string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.RemoteAddr = remoteAddr
	ctx := contextWithIdentity(req, &auth.Identity{Subject: subject})
	return req.WithContext(ctx)
}
