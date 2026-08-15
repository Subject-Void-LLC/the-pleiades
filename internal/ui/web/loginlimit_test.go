// Tests for the login rate limiter.
//
// It runs against a real api.RateLimiter rather than a double, per RULE 0:
// the whole claim is that this route CONSUMES the shared token bucket
// instead of growing a second one, and a fake bucket would prove the
// opposite of that.
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/go-chi/chi/v5"
)

func newLimitedProbe(t *testing.T, cfg api.RateLimiterConfig) *loginProbe {
	t.Helper()
	registerTestView()

	issuer := authtest.New(t, "ui-login-test", "ui-login-audience")
	store := newMemStore()
	cookie := session.CookieCodec{Insecure: true}

	h := New(Config{
		Prefix:       "/ui",
		Sessions:     store,
		Cookie:       cookie,
		Tokens:       issuer.Evaluator(),
		LoginLimiter: api.NewRateLimiter(cfg),
		HATEOAS:      permitEverything{},
		Admission:    allowAll{},
	})
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())

	return &loginProbe{probe: &probe{mux: root, store: store, cookie: cookie}, issuer: issuer}
}

// TestLoginRateLimit_ShedsABurstFromOneSource is the property. Without it,
// an attacker gets as many password guesses per second as they have
// bandwidth for, bounded only by the per-account lockout, which does not
// help at all when they are spraying one password across many addresses.
func TestLoginRateLimit_ShedsABurstFromOneSource(t *testing.T) {
	// Burst of two so the test is three requests rather than a hundred.
	p := newLimitedProbe(t, api.RateLimiterConfig{RequestsPerSecond: 0.5, Burst: 2})

	var lastCode int
	for i := range 3 {
		rec := p.postFormRaw("/ui/login", "token=irrelevant")
		lastCode = rec.Code
		if i < 2 && rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was shed, but the burst allows 2", i+1)
		}
	}

	if lastCode != http.StatusTooManyRequests {
		t.Errorf("the third request returned %d, want 429; the limiter is not shedding", lastCode)
	}
}

// TestLoginRateLimit_RunsBeforeTheCSRFCheck proves the ordering.
//
// It matters: the limiter exists to shed load, and a flood that got as far
// as a CSRF check, a database read and an Argon2id derivation before being
// refused would have already cost everything the limiter was installed to
// save. The probe posts WITHOUT a CSRF pair, so a request that reaches the
// CSRF layer answers 403; one shed by the limiter answers 429.
func TestLoginRateLimit_RunsBeforeTheCSRFCheck(t *testing.T) {
	p := newLimitedProbe(t, api.RateLimiterConfig{RequestsPerSecond: 0.5, Burst: 1})

	first := p.postFormRaw("/ui/login", "token=irrelevant")
	if first.Code != http.StatusForbidden {
		t.Fatalf("first request = %d, want 403 from the CSRF layer", first.Code)
	}

	second := p.postFormRaw("/ui/login", "token=irrelevant")
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request = %d, want 429; the CSRF check ran before the limiter, "+
			"so a flood pays for a CSRF check before being shed", second.Code)
	}
}

// TestLoginRateLimit_ServesTheLoginPageRatherThanJSON covers why this does
// not simply reuse api.RateLimitMiddleware.
//
// That middleware answers with RespondError, which writes JSON. The only
// client of this route is a browser, and a JSON body rendered in a browser
// window is a dead end for the operator who typed too fast.
func TestLoginRateLimit_ServesTheLoginPageRatherThanJSON(t *testing.T) {
	p := newLimitedProbe(t, api.RateLimiterConfig{RequestsPerSecond: 0.5, Burst: 1})

	p.postFormRaw("/ui/login", "token=irrelevant")
	shed := p.postFormRaw("/ui/login", "token=irrelevant")

	if shed.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", shed.Code)
	}
	if ct := shed.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want HTML", ct)
	}
	if !strings.Contains(shed.Body.String(), "Sign in") {
		t.Error("the shed response is not the sign-in page, so a rate-limited operator has nowhere to go")
	}
}

// TestLoginRateLimit_AbsentLimiterIsNotAFailure covers the optional field.
// A single-operator deployment may legitimately run without one.
func TestLoginRateLimit_AbsentLimiterIsNotAFailure(t *testing.T) {
	p := newLoginProbe(t) // no LoginLimiter configured

	for range 5 {
		if rec := p.postFormRaw("/ui/login", "token=irrelevant"); rec.Code == http.StatusTooManyRequests {
			t.Fatal("requests were shed with no limiter configured")
		}
	}
}

// TestLoginRateLimit_DoesNotThrottleTheLoginPageItself proves the limiter
// is scoped to the POST.
//
// Throttling GET /ui/login would mean a shed request re-renders a page that
// is itself shed, and the operator sees nothing at all.
func TestLoginRateLimit_DoesNotThrottleTheLoginPageItself(t *testing.T) {
	p := newLimitedProbe(t, api.RateLimiterConfig{RequestsPerSecond: 0.5, Burst: 1})

	for i := range 5 {
		rec := p.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /ui/login request %d = %d, want 200", i+1, rec.Code)
		}
	}
}

// A compile-time proof that the limiter field takes the SHARED type rather
// than a private one, which is what "consumed rather than reimplemented"
// has to mean if the Adversarial Pattern Justification is to be checkable.
var _ = func() *api.RateLimiter { return Config{}.LoginLimiter }
