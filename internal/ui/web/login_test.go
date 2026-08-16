package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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
	cookie := session.CookieCodec{}

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

// postForm submits the sign-in form the way a browser does: fetch the page
// first, then send back the CSRF cookie and hidden field it carried.
//
// The two-step is not ceremony. POST /ui/login is now behind preAuthCSRF,
// and a probe that skipped the GET would be testing a request no browser
// ever makes. It would also silently pass if the CSRF layer were deleted,
// which is the failure mode that matters most here.
func (p *loginProbe) postForm(target, body string) *httptest.ResponseRecorder {
	cookie, csrf := p.loginFormCredentials()
	if csrf != "" {
		body = appendField(body, session.CSRFField, csrf)
	}

	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	return p.serve(r)
}

// postFormRaw submits without the CSRF pair, for the tests whose subject IS
// the refusal.
func (p *loginProbe) postFormRaw(target, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return p.serve(r)
}

// loginFormCredentials renders the sign-in page and reads back the pair it
// issued: the cookie from Set-Cookie, and the token from the hidden field in
// the HTML.
//
// It parses the real response rather than calling issuePreAuthCookie
// directly, so the test proves the two halves a browser sees actually agree.
// Deriving both from the same helper would prove only that the helper is
// self-consistent.
func (p *loginProbe) loginFormCredentials() (*http.Cookie, string) {
	w := p.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil))

	var preAuth *http.Cookie
	for _, c := range w.Result().Cookies() {
		if strings.Contains(c.Name, preAuthCookieName) {
			preAuth = c
		}
	}
	return preAuth, hiddenFieldValue(w.Body.String(), session.CSRFField)
}

// hiddenFieldValue pulls one hidden input's value out of rendered HTML.
func hiddenFieldValue(body, name string) string {
	marker := `name="` + name + `" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// appendField adds one urlencoded field to a form body.
func appendField(body, name, value string) string {
	field := name + "=" + url.QueryEscape(value)
	if body == "" {
		return field
	}
	return body + "&" + field
}

// sessionCookie picks the session cookie out of a response by NAME.
//
// By name rather than by position, because a sign-in response now carries
// two Set-Cookie headers: the pre-auth CSRF cookie being cleared, and the
// session being written. Indexing would silently follow whichever order the
// handler happens to emit them in, and would have kept passing while
// asserting nothing about the cookie it meant.
func (p *loginProbe) sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == p.cookie.Name() {
			return c
		}
	}
	return nil
}

// withoutCSRFToken blanks the pre-auth CSRF token in rendered HTML.
//
// Two failed sign-ins render a different token because each render mints a
// fresh one, which is correct: reusing a token across an unbounded number of
// retries is what a re-render is the natural moment to avoid. The token is
// random and carries no information about WHY a login failed, so comparing
// two failure pages means comparing everything except it. Without this, the
// indistinguishability test would be asserting that a CSRF token is not
// random, which is the opposite of what it wants.
// It replaces the token VALUE everywhere it occurs rather than editing one
// attribute, because the same value is rendered twice: in the form's hidden
// field and in the layout's hx-headers attribute. Stripping only the field
// would leave the second copy varying and the comparison would still be
// asserting that a random value is not random.
func withoutCSRFToken(body string) string {
	token := hiddenFieldValue(body, session.CSRFField)
	if token == "" {
		return body
	}
	return strings.ReplaceAll(body, token, "<csrf>")
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
	c := p.sessionCookie(rec)
	if c == nil {
		t.Fatal("no session cookie was set")
	}
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
			// A fresh pre-auth CSRF cookie is expected on a re-render and
			// is not a credential. What must never appear is a session.
			if p.sessionCookie(rec) != nil {
				t.Error("a failed login still minted a session cookie")
			}
			bodies = append(bodies, withoutCSRFToken(rec.Body.String()))
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

	// The CSRF pair is supplied so the request gets PAST preAuthCSRF and
	// actually reaches the ParseForm branch this test is named for. Without
	// it the refusal would be a 403 and the branch would go uncovered while
	// the test still looked like it passed something.
	cookie, csrf := p.loginFormCredentials()
	r := httptest.NewRequest(http.MethodPost, "/ui/login",
		strings.NewReader("%zz&"+session.CSRFField+"="+csrf))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	if rec := p.serve(r); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestLogin_RefusesAPostWithoutTheCSRFPair is the guard the two-step probe
// above exists to keep honest.
//
// Login CSRF forces a victim's browser to sign in as the ATTACKER, so the
// victim then works inside an account the attacker controls and can read.
// It was low value when the only credential was a token the attacker had to
// already hold; a password login makes it worth defending.
func TestLogin_RefusesAPostWithoutTheCSRFPair(t *testing.T) {
	p := newLoginProbe(t)
	token := p.issuer.Token(t, &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator})

	// A perfectly valid credential, submitted without the pair.
	rec := p.postFormRaw("/ui/login", "token="+token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if p.sessionCookie(rec) != nil {
		t.Error("a CSRF-refused login still minted a session")
	}
}

// TestLogin_RefusesACrossSiteFetch covers the stateless layer, which is
// strictly stronger than the token against several bypass classes.
func TestLogin_RefusesACrossSiteFetch(t *testing.T) {
	p := newLoginProbe(t)
	token := p.issuer.Token(t, &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator})

	cookie, csrf := p.loginFormCredentials()
	r := httptest.NewRequest(http.MethodPost, "/ui/login",
		strings.NewReader("token="+token+"&"+session.CSRFField+"="+csrf))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.AddCookie(cookie)

	rec := p.serve(r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if p.sessionCookie(rec) != nil {
		t.Error("a cross-site login still minted a session")
	}
}

// TestLogin_RefusesAMismatchedCSRFToken proves the pair must actually
// agree, rather than merely both being present.
func TestLogin_RefusesAMismatchedCSRFToken(t *testing.T) {
	p := newLoginProbe(t)
	token := p.issuer.Token(t, &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator})

	// The cookie from one rendered form, the token from a different one.
	cookie, _ := p.loginFormCredentials()
	_, otherCSRF := p.loginFormCredentials()

	r := httptest.NewRequest(http.MethodPost, "/ui/login",
		strings.NewReader("token="+token+"&"+session.CSRFField+"="+otherCSRF))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)

	if rec := p.serve(r); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// TestLogout_ClearsTheCookieAndRedirectsToLogin completes the pair.
func TestLogout_ClearsTheCookieAndRedirectsToLogin(t *testing.T) {
	p := newLoginProbe(t)
	token := p.issuer.Token(t, &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator})

	login := p.postForm("/ui/login", "token="+token)
	sessCookie := p.sessionCookie(login)
	if sessCookie == nil {
		t.Fatal("login minted no session cookie")
	}

	req := httptest.NewRequest(http.MethodPost, "/ui/logout", nil)
	req.AddCookie(sessCookie)
	// The session token is the cookie value, and CSRF is derived from it.
	sess, err := p.store.Resolve(t.Context(), sessCookie.Value)
	if err != nil {
		t.Fatalf("resolving the new session: %v", err)
	}
	req.Header.Set("X-CSRF-Token", session.CSRFToken(sess.CSRFKey, sessCookie.Value))

	rec := p.serve(req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/ui/login" {
		t.Errorf("Location = %q, want /ui/login", loc)
	}
	// Deleted rather than flagged, so the credential stops working
	// immediately everywhere rather than when a cache notices.
	if _, err := p.store.Resolve(t.Context(), sessCookie.Value); err == nil {
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
