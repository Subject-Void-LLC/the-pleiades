package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/go-chi/chi/v5"
)

// This file covers the login exchange and the preference controls, both of
// which the record and refusal suites route around.
//
// Login is exercised through a real auth.Evaluator over real signed tokens,
// because the entire argument for this endpoint is that it mints no
// credential of its own: it validates through the same evaluator the Bearer
// path uses. A stub validator would remove the only claim worth proving.

// loginProbe is a probe whose handler also carries a token evaluator, which
// newProbe deliberately omits since nothing else in the package needs one.
type loginProbe struct {
	*probe
	issuer *authtest.Issuer
}

func newLoginProbe(t *testing.T) *loginProbe {
	t.Helper()
	registerTestView()

	issuer := authtest.New(t, "ui-login-test", "ui-login-audience")
	store := newMemStore()
	cookie := session.CookieCodec{Insecure: true}

	h := New(Config{
		Prefix:    "/ui",
		Sessions:  store,
		Cookie:    cookie,
		Tokens:    issuer.Evaluator(),
		HATEOAS:   permitEverything{},
		Admission: allowAll{},
	})
	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())

	return &loginProbe{probe: &probe{mux: root, store: store, cookie: cookie}, issuer: issuer}
}

// postForm submits an unauthenticated form, which is what the login page is.
func (p *loginProbe) postForm(target, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return p.serve(r)
}

// TestLogin_ValidTokenMintsASessionCookie is the whole feature: it converts a
// credential an EventSource cannot send into one it can.
func TestLogin_ValidTokenMintsASessionCookie(t *testing.T) {
	p := newLoginProbe(t)
	token := p.issuer.Token(t, &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator})

	rec := p.postForm("/ui/login", "token="+token)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/ui" {
		t.Errorf("Location = %q, want /ui", loc)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie was set")
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("the session cookie is not HttpOnly, so script can read it")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	// The response must not echo the submitted token back. It is a bearer
	// credential, and a page that renders it puts it in browser history, in
	// a proxy log, and in a screenshot.
	if strings.Contains(rec.Body.String(), token) {
		t.Error("the login response echoed the submitted token")
	}

	// The session it minted must actually authenticate a subsequent request.
	req := httptest.NewRequest(http.MethodGet, "/ui/"+testView, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if got := p.serve(req); got.Code != http.StatusOK {
		t.Errorf("the minted session did not authenticate a read: status = %d", got.Code)
	}
}

// TestLogin_EveryFailureLooksTheSame is the security property. Telling a
// caller which of malformed, expired or wrongly-signed they achieved tells an
// attacker which one they achieved.
func TestLogin_EveryFailureLooksTheSame(t *testing.T) {
	p := newLoginProbe(t)
	other := authtest.New(t, "someone-else", "someone-elses-audience")

	bodies := []string{}
	for _, tc := range []struct{ name, form string }{
		{"no token", "token="},
		{"whitespace only", "token=%20%20"},
		{"not a jwt at all", "token=not-a-token"},
		{"signed by a different issuer", "token=" + other.Token(t, &auth.Identity{Subject: "attacker"})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := p.postForm("/ui/login", tc.form)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if len(rec.Result().Cookies()) != 0 {
				t.Error("a failed login still set a cookie")
			}
			bodies = append(bodies, rec.Body.String())
		})
	}

	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Error("two different login failures rendered different pages, which distinguishes them to an attacker")
			break
		}
	}
}

// TestLogin_PageRendersWithoutASession proves the sign-in page shares the
// chrome without offering to sign out of nothing.
func TestLogin_PageRendersWithoutASession(t *testing.T) {
	p := newLoginProbe(t)

	rec := p.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `type="password"`) {
		t.Error("the token field is not a password field")
	}
	// Paste-blocking a 900-character JWT is hostile and is its own
	// accessibility failure (WCAG SC 3.3.8).
	if strings.Contains(body, "onpaste") {
		t.Error("the login page blocks paste")
	}
	if strings.Contains(body, "/ui/logout") {
		t.Error("the login page offers to sign out of nothing")
	}
}

// TestLogin_MalformedFormBodyIsRejected covers the ParseForm branch.
func TestLogin_MalformedFormBodyIsRejected(t *testing.T) {
	p := newLoginProbe(t)

	r := httptest.NewRequest(http.MethodPost, "/ui/login", strings.NewReader("%zz"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := p.serve(r); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestLogout_ClearsTheCookieAndRedirectsToLogin completes the pair.
func TestLogout_ClearsTheCookieAndRedirectsToLogin(t *testing.T) {
	p := newLoginProbe(t)
	token := p.issuer.Token(t, &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator})

	login := p.postForm("/ui/login", "token="+token)
	cookies := login.Result().Cookies()

	req := httptest.NewRequest(http.MethodPost, "/ui/logout", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	// The session token is the cookie value, and CSRF is derived from it.
	sess, err := p.store.Resolve(t.Context(), cookies[0].Value)
	if err != nil {
		t.Fatalf("resolving the new session: %v", err)
	}
	req.Header.Set("X-CSRF-Token", session.CSRFToken(sess.CSRFKey, cookies[0].Value))

	rec := p.serve(req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/login" {
		t.Errorf("Location = %q, want /ui/login", loc)
	}
	// Deleted rather than flagged, so the credential stops working
	// immediately everywhere rather than when a cache notices.
	if _, err := p.store.Resolve(t.Context(), cookies[0].Value); err == nil {
		t.Error("the session row survived logout")
	}
}

// TestPreferences_RejectUndeclaredValues proves the appearance controls are
// a closed vocabulary rather than free text. The values reach a data
// attribute on <html>, so an unvalidated one is an attribute injection.
func TestPreferences_RejectUndeclaredValues(t *testing.T) {
	p := newRecordProbe(t)

	for _, tc := range []struct{ path, body string }{
		{"/ui/theme", "theme=chartreuse"},
		{"/ui/skin", "skin=comic-sans"},
		{"/ui/a11y", "a11y=maybe"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := p.post(t, tc.path, tc.body)
			// Either refused outright or normalised to a declared value.
			// What must never happen is the submitted string coming back.
			for _, c := range rec.Result().Cookies() {
				if strings.Contains(c.Value, "chartreuse") ||
					strings.Contains(c.Value, "comic-sans") ||
					strings.Contains(c.Value, "maybe") {
					t.Errorf("an undeclared preference %q was stored verbatim", c.Value)
				}
			}
		})
	}
}
