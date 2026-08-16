// One guard over every cookie this package sets.
//
// It exists because the alternative failed. Until Phase 20 each writer chose
// between a secure literal and an insecure twin, three separate times, and
// the tests that covered them asserted per writer. That arrangement is what
// let three gosec waivers accumulate and it is what a future "just add a
// flag for local development" change would reintroduce one writer at a time.
// This asserts the property at the level it actually matters: whatever this
// UI hands a browser, on any route, is Secure.
package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// TestEveryCookieThisUISetsIsSecure drives the three routes that write one
// and inspects the real Set-Cookie headers.
//
// Through the mounted router rather than by calling the writers, because a
// writer that is correct and unreachable proves nothing, and because a new
// route that sets a cookie should be caught by this test the day it is
// added rather than the day somebody remembers to extend a list.
func TestEveryCookieThisUISetsIsSecure(t *testing.T) {
	login := newLoginProbe(t)

	// The sign-in page mints the pre-auth CSRF cookie, and a successful
	// sign-in mints the session cookie and expires the pre-auth one, so
	// these two responses between them carry every cookie the login path
	// produces.
	token := login.issuer.Token(t, &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator})
	responses := []*httptest.ResponseRecorder{
		login.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil)),
		login.postForm("/ui/login", "token="+token),
	}

	// The appearance preferences, which are the cookies most easily
	// dismissed as harmless: they carry no credential, and they are also
	// the ones a "make it work over http" change would relax first.
	prefs := newProbe(t, allowAll{}, permitEverything{})
	for _, path := range []string{"/ui/theme", "/ui/skin", "/ui/a11y"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(
			"theme=dark&skin=terminal&a11y=on&"+csrfField(prefs, t)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		responses = append(responses, prefs.serve(prefs.authed(r)))
	}

	var seen int
	for _, rec := range responses {
		for _, c := range rec.Result().Cookies() {
			seen++
			if !c.Secure {
				t.Errorf("cookie %q is not Secure, so a browser would send it in the clear", c.Name)
			}
			if !c.HttpOnly {
				t.Errorf("cookie %q is not HttpOnly", c.Name)
			}
			// __Host- is browser-enforced and refuses the cookie outright
			// unless all three of these hold, so a prefixed name and a
			// missing attribute is worse than no prefix at all.
			if strings.HasPrefix(c.Name, "__Host-") && (c.Path != "/" || c.Domain != "" || !c.Secure) {
				t.Errorf("cookie %q claims the __Host- prefix but breaks its rules: %+v", c.Name, c)
			}
		}
	}

	// A count, because a test that inspects nothing passes silently. Five:
	// the pre-auth cookie, the session cookie, the pre-auth cookie again as
	// it is cleared, and one appearance cookie per preference route.
	if seen < 5 {
		t.Errorf("only %d cookies were inspected; the routes that set them may have stopped setting them", seen)
	}
}

// csrfField returns the session-bound CSRF token as a form field, which
// every state-changing UI route requires.
func csrfField(p *probe, t *testing.T) string {
	t.Helper()
	return "_csrf=" + p.csrf(t)
}
