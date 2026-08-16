package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	"github.com/go-chi/chi/v5"
)

// These tests drive this package's own handlers directly, over a minimal
// registered view.
//
// The conformance suite in internal/ui/resources covers the same handlers
// against every real resource, which is the stronger claim about the
// application. This file covers what that suite deliberately does not
// reach: the refusal paths. A caller with no session, a submission with a
// forged token, a cross-site fetch, a resource that does not exist. Those
// are the branches where a mistake is a security defect rather than a
// broken page, and none of them is on any resource's happy path.

// testView is registered once for this package's test binary.
const testView = "probes"

var registerTestView = sync.OnceFunc(func() {
	view.MustRegister(view.Descriptor{
		Name:     testView,
		Title:    "Probes",
		NavLabel: "PROBES",
		NavOrder: 10,
		Summary:  "A minimal view existing only to drive this package's handlers.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, Required: true,
				MaxLen: 64, Autocomplete: "off", InList: true, InForm: true, MobilePrimary: true},
		},
		Ops: view.Ops{
			List:   &apispec.ListDevices,
			Get:    &apispec.GetDevice,
			Create: &apispec.CreateDevice,
			Update: &apispec.UpdateDevice,
			Delete: &apispec.DeleteDevice,
		},
		Handlers: view.MustBind[string](probeReader{}, probeWriter{}, view.Projector[string]{
			Row:  func(s string) view.Row { return view.Row{ID: s, Cells: view.Cells{"name": s}} },
			Form: func(s string) map[string]string { return map[string]string{"name": s} },
			Bind: func(v view.Values) (string, view.FieldErrors) { return v.Get("name"), view.FieldErrors{} },
		}),
	})
})

type probeReader struct{}

func (probeReader) List(context.Context, view.Query) (view.Page[string], error) {
	return view.Page[string]{Items: []string{"probe-1"}}, nil
}
func (probeReader) Get(_ context.Context, id string) (string, error) { return id, nil }

type probeWriter struct{}

func (probeWriter) Create(context.Context, string) (string, error) { return "probe-1", nil }
func (probeWriter) Update(context.Context, string, string) error   { return nil }
func (probeWriter) Delete(context.Context, string) error           { return nil }

// memStore is the smallest session.Store that satisfies these tests.
type memStore struct {
	mu   sync.Mutex
	rows map[string]session.Session
}

func newMemStore() *memStore { return &memStore{rows: map[string]session.Session{}} }

func (s *memStore) Create(_ context.Context, id *auth.Identity, _, absolute time.Duration) (string, error) {
	token, err := session.NewToken()
	if err != nil {
		return "", err
	}
	key, err := session.NewCSRFKey()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Scopes are persisted, like the real ent store does. Dropping them
	// here made this double quietly lossy: every test in the package would
	// have passed while a session that reached the database with no
	// authority at all looked identical to one that reached it with the
	// right authority. That is LESSONS_LEARNED #94's shape, a fixture whose
	// presence disguises the gap by making the tests look thorough.
	scopes := make([]string, 0, len(id.Scopes))
	for _, scope := range id.Scopes {
		scopes = append(scopes, string(scope))
	}

	s.rows[token] = session.Session{
		Subject:           id.Subject,
		Role:              id.Role,
		Scopes:            scopes,
		CSRFKey:           key,
		IdleExpiresAt:     time.Now().Add(absolute),
		AbsoluteExpiresAt: time.Now().Add(absolute),
	}
	return token, nil
}

func (s *memStore) Resolve(_ context.Context, token string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[token]
	if !ok {
		return session.Session{}, session.ErrNotFound
	}
	return row, nil
}

func (s *memStore) Touch(context.Context, string, time.Duration) error { return nil }

func (s *memStore) Delete(_ context.Context, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, token)
	return nil
}

func (s *memStore) DeleteExpired(context.Context, time.Time) (int, error) { return 0, nil }

// count reports how many sessions exist, for tests asserting that a
// refused request minted none.
func (s *memStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

// DeleteForSubject is implemented for real rather than stubbed, because the
// password-change tests assert on what it actually removed. A stub returning
// (0, nil) would let a change that revoked nothing pass as one that revoked
// everything, which is the same class of lossy double that hid the dropped
// Scopes field above.
func (s *memStore) DeleteForSubject(_ context.Context, subject, keepToken string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := 0
	for token, row := range s.rows {
		if row.Subject != subject || token == keepToken {
			continue
		}
		delete(s.rows, token)
		deleted++
	}
	return deleted, nil
}

// allowAll and denyAll are the two admission answers, so a test can name
// which one it is exercising instead of arranging an identity to produce it.
type allowAll struct{}

func (allowAll) Evaluate(context.Context, *auth.Identity, auth.AdmissionRequest) error { return nil }

type denyAll struct{}

func (denyAll) Evaluate(context.Context, *auth.Identity, auth.AdmissionRequest) error {
	return context.Canceled
}

// permitEverything is a HATEOAS generator that permits every candidate, so
// affordance-gated controls render and the refusal tests are about the
// refusal rather than about an empty permitted set.
type permitEverything struct{}

func (permitEverything) Permitted(_ context.Context, _ *auth.Identity, candidates []auth.Affordance) ([]auth.LinkRel, error) {
	rels := make([]auth.LinkRel, 0, len(candidates))
	for _, c := range candidates {
		rels = append(rels, c.Rel)
	}
	return rels, nil
}

// failingGenerator is the case HTML has no way to express: authorization
// could not be evaluated at all.
type failingGenerator struct{}

func (failingGenerator) Permitted(context.Context, *auth.Identity, []auth.Affordance) ([]auth.LinkRel, error) {
	return nil, context.DeadlineExceeded
}

type probe struct {
	mux    http.Handler
	store  *memStore
	cookie session.CookieCodec
	token  string
}

func newProbe(t *testing.T, admission api.Admitter, hateoas auth.HATEOASGenerator) *probe {
	t.Helper()
	registerTestView()

	store := newMemStore()
	token, err := store.Create(t.Context(), &auth.Identity{Subject: "prober", Role: auth.RoleAdmin}, time.Hour, time.Hour)
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}

	cookie := session.CookieCodec{}
	h := New(Config{
		Prefix:    "/ui",
		Sessions:  store,
		Cookie:    cookie,
		HATEOAS:   hateoas,
		Admission: admission,
	})

	root := chi.NewRouter()
	root.Mount("/ui", h.Routes())

	return &probe{mux: root, store: store, cookie: cookie, token: token}
}

func (p *probe) authed(r *http.Request) *http.Request {
	w := httptest.NewRecorder()
	p.cookie.Write(w, p.token)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func (p *probe) csrf(t *testing.T) string {
	t.Helper()
	sess, err := p.store.Resolve(t.Context(), p.token)
	if err != nil {
		t.Fatalf("resolving the probe session: %v", err)
	}
	return session.CSRFToken(sess.CSRFKey, p.token)
}

func (p *probe) serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	p.mux.ServeHTTP(w, r)
	return w
}

// TestRequireSession_RedirectsAnonymousCallers covers both answers the same
// condition owes: a document request gets a redirect, a fragment request
// gets the header that tells HTMX to navigate rather than swap.
func TestRequireSession_RedirectsAnonymousCallers(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	w := p.serve(httptest.NewRequest(http.MethodGet, "/ui/"+testView, nil))
	if w.Code != http.StatusSeeOther {
		t.Errorf("anonymous document request = %d, want 303", w.Code)
	}

	r := httptest.NewRequest(http.MethodGet, "/ui/"+testView, nil)
	r.Header.Set("HX-Request", "true")
	fragment := p.serve(r)

	if fragment.Header().Get("HX-Redirect") == "" {
		t.Error("an anonymous fragment request carries no HX-Redirect, so a login page " +
			"would be swapped into whatever element triggered it")
	}
	if fragment.Code != http.StatusNoContent {
		t.Errorf("anonymous fragment request = %d, want 204", fragment.Code)
	}
}

// TestCSRF_RefusesWithoutAValidToken walks every way a state-changing
// request can fail to prove it came from this application.
func TestCSRF_RefusesWithoutAValidToken(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		headers map[string]string
	}{
		{"no token at all", "name=x", nil},
		{"empty token", "name=x&_csrf=", nil},
		{"forged token", "name=x&_csrf=not-a-real-token", nil},
		{"cross-site fetch", "name=x", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"same-site but not same-origin", "name=x", map[string]string{"Sec-Fetch-Site": "same-site"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProbe(t, allowAll{}, permitEverything{})

			r := httptest.NewRequest(http.MethodPost, "/ui/"+testView, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			p.authed(r)

			if w := p.serve(r); w.Code != http.StatusForbidden {
				t.Errorf("POST %s = %d, want 403", tc.name, w.Code)
			}
		})
	}
}

// TestCSRF_AcceptsAValidToken is the other half. A refusal test with no
// matching acceptance test cannot distinguish "the check works" from "every
// write is broken".
func TestCSRF_AcceptsAValidToken(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	r := httptest.NewRequest(http.MethodPost, "/ui/"+testView,
		strings.NewReader("name=accepted&_csrf="+p.csrf(t)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	p.authed(r)

	if w := p.serve(r); w.Code != http.StatusSeeOther {
		t.Errorf("POST with a valid token = %d, want 303", w.Code)
	}
}

// TestCSRF_AcceptsTheHeaderForm covers HTMX's path, which sends the token as
// a header rather than a form field.
func TestCSRF_AcceptsTheHeaderForm(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	r := httptest.NewRequest(http.MethodPost, "/ui/"+testView, strings.NewReader("name=hx"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set(session.CSRFHeader, p.csrf(t))
	p.authed(r)

	if w := p.serve(r); w.Code != http.StatusSeeOther {
		t.Errorf("POST with a header token = %d, want 303", w.Code)
	}
}

// TestReadsAreNotCSRFChecked: a GET that demanded a token would break every
// bookmark and every link into the application.
func TestReadsAreNotCSRFChecked(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	if w := p.serve(p.authed(httptest.NewRequest(http.MethodGet, "/ui/"+testView, nil))); w.Code != http.StatusOK {
		t.Errorf("authenticated GET = %d, want 200", w.Code)
	}
}

// TestScopeRefusalIsForbiddenNotNotFound distinguishes the two failures a
// caller can hit, because conflating them makes a permission problem look
// like a typo.
func TestScopeRefusalIsForbiddenNotNotFound(t *testing.T) {
	denied := newProbe(t, denyAll{}, permitEverything{})
	if w := denied.serve(denied.authed(httptest.NewRequest(http.MethodGet, "/ui/"+testView, nil))); w.Code != http.StatusForbidden {
		t.Errorf("a scope refusal = %d, want 403", w.Code)
	}

	allowed := newProbe(t, allowAll{}, permitEverything{})
	if w := allowed.serve(allowed.authed(httptest.NewRequest(http.MethodGet, "/ui/no-such-view", nil))); w.Code != http.StatusNotFound {
		t.Errorf("an unregistered view = %d, want 404", w.Code)
	}
}

// TestAffordanceFailureRendersAnAlertAndNoButtons is FAILURE_PATTERNS #73 in
// a new medium.
//
// HTML cannot express the JSON API's "the _links field is absent, which is
// not the same as empty". So when authorization cannot be evaluated, the
// page must render no action controls *and* say so -- rather than quietly
// presenting a read-only page as though that were the answer.
func TestAffordanceFailureRendersAnAlertAndNoButtons(t *testing.T) {
	p := newProbe(t, allowAll{}, failingGenerator{})

	w := p.serve(p.authed(httptest.NewRequest(http.MethodGet, "/ui/"+testView, nil)))
	if w.Code != http.StatusOK {
		t.Fatalf("GET with a failing generator = %d, want 200", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "could not be determined") {
		t.Error("no alert is rendered, so the page silently claims the caller may do nothing")
	}
	if strings.Contains(body, `href="/ui/`+testView+`/new"`) {
		t.Error("a create control rendered even though authorization could not be evaluated")
	}
}

// TestSecurityHeaders covers the policy every response in the subtree
// carries, including the login page an anonymous caller reaches.
func TestSecurityHeaders(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	for _, path := range []string{"/ui/login", "/ui/" + testView} {
		w := p.serve(p.authed(httptest.NewRequest(http.MethodGet, path, nil)))

		csp := w.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatalf("%s carries no content security policy", path)
		}
		if strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("%s permits unsafe script or style: %s", path, csp)
		}
		for _, want := range []string{
			"default-src 'none'", "script-src 'self'", "style-src 'self'",
			"frame-ancestors 'none'", "base-uri 'none'", "form-action 'self'",
		} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s policy is missing %q", path, want)
			}
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s does not set nosniff", path)
		}
		if w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s does not set Referrer-Policy: no-referrer", path)
		}
	}
}

// TestUndeclaredFieldsAreRefused: silently dropping input a caller believed
// was accepted is how somebody ends up certain they changed something they
// did not.
func TestUndeclaredFieldsAreRefused(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	r := httptest.NewRequest(http.MethodPost, "/ui/"+testView,
		strings.NewReader("name=x&is_admin=true&_csrf="+p.csrf(t)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	p.authed(r)

	if w := p.serve(r); w.Code != http.StatusBadRequest {
		t.Errorf("POST with an undeclared field = %d, want 400", w.Code)
	}
}

// TestValidationFailureIs422WithAFocusableSummary covers the redisplay path,
// including that what was typed comes back rather than being cleared.
func TestValidationFailureIs422WithAFocusableSummary(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	r := httptest.NewRequest(http.MethodPost, "/ui/"+testView,
		strings.NewReader("name=&_csrf="+p.csrf(t)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	p.authed(r)

	w := p.serve(r)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST with an empty required field = %d, want 422", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, `id="error-summary"`) || !strings.Contains(body, `tabindex="-1"`) {
		t.Error("the error summary is missing or not focusable")
	}
	if !strings.Contains(body, `aria-invalid="true"`) {
		t.Error("the offending control is not marked aria-invalid")
	}
}

// TestLogoutDeletesTheSessionRow proves revocation is immediate, which is
// the whole reason this UI holds a server-side session rather than a signed
// token nothing can withdraw.
func TestLogoutDeletesTheSessionRow(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	r := httptest.NewRequest(http.MethodPost, "/ui/logout", strings.NewReader("_csrf="+p.csrf(t)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	p.authed(r)

	if w := p.serve(r); w.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d, want 303", w.Code)
	}
	if _, err := p.store.Resolve(t.Context(), p.token); err == nil {
		t.Error("the session row survived logout, so the credential is still usable")
	}
}

// TestAppearanceControlsRoundTrip covers the three preference axes, each of
// which is a real form post so it works with scripting disabled.
func TestAppearanceControlsRoundTrip(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	for _, tc := range []struct{ path, field, value, cookie string }{
		{"/ui/theme", "theme", "dark", themeCookieName},
		{"/ui/skin", "skin", "las-ventanas", skinCookieName},
		{"/ui/a11y", "a11y", "on", a11yCookieName},
	} {
		t.Run(tc.field, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.path,
				strings.NewReader(tc.field+"="+tc.value+"&_csrf="+p.csrf(t)))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			p.authed(r)

			w := p.serve(r)
			if w.Code != http.StatusSeeOther {
				t.Fatalf("POST %s = %d, want 303", tc.path, w.Code)
			}

			var found bool
			for _, c := range w.Result().Cookies() {
				if c.Name == tc.cookie && c.Value == tc.value {
					found = true
					if !c.HttpOnly {
						t.Error("a preference cookie is script-readable for no benefit")
					}
				}
			}
			if !found {
				t.Errorf("POST %s set no %s cookie", tc.path, tc.cookie)
			}
		})
	}
}

// TestIndexRedirectsToTheFirstReachableView, rather than to a hardcoded
// landing page a caller may have no access to.
func TestIndexRedirectsToTheFirstReachableView(t *testing.T) {
	p := newProbe(t, allowAll{}, permitEverything{})

	w := p.serve(p.authed(httptest.NewRequest(http.MethodGet, "/ui/", nil)))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("GET /ui/ = %d, want 303", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/ui/") {
		t.Errorf("Location = %q, want a view under /ui/", loc)
	}
}
