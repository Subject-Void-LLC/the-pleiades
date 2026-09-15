// This file covers how an appearance preference is read back off a request
// and what happens when the form carrying one is malformed.
//
// The readers are three lines each and look too small to be worth a test,
// which is exactly why they are worth one: each decides what a page renders
// as for a caller who has never chosen anything, and each is the single
// place a bad cookie value is prevented from reaching a CSS class name. A
// wrong answer here is not a crash, it is every page silently rendering in
// the wrong palette, or a skin attribute carrying whatever somebody put in
// their own cookie jar.
package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// requestWithCookie builds a request carrying one cookie.
func requestWithCookie(name, value string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/ui/devices", nil)
	if name != "" {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return r
}

// TestThemeOf_FallsBackToSystem covers the appearance axis.
//
// A caller who has never chosen must get the system default rather than a
// hardcoded light or dark, because guessing wrong means a reader in a dark
// room gets a white page.
func TestThemeOf_FallsBackToSystem(t *testing.T) {
	h := &Handler{}

	for _, tc := range []struct {
		name, cookie, value, want string
	}{
		{"no cookie at all", "", "", string(view.ThemeSystem)},
		{"an empty value", themeCookieName, "", string(view.ThemeSystem)},
		// The value is attacker-controlled: it is whatever is in the
		// caller's own cookie jar, and it ends up in a rendered
		// attribute. ParseTheme is what keeps it inside the closed set.
		{"a value nothing declares", themeCookieName, "neon-green", string(view.ThemeSystem)},
		{"an injection attempt", themeCookieName, `" onload="alert(1)`, string(view.ThemeSystem)},
		{"a real choice", themeCookieName, string(view.ThemeDark), string(view.ThemeDark)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.themeOf(requestWithCookie(tc.cookie, tc.value)); got != tc.want {
				t.Errorf("themeOf() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSkinOf_FallsBackToBrutalist covers the theme-family axis, whose
// default is a deliberate choice rather than a system question.
func TestSkinOf_FallsBackToBrutalist(t *testing.T) {
	h := &Handler{}

	for _, tc := range []struct {
		name, cookie, value, want string
	}{
		{"no cookie at all", "", "", string(view.SkinBrutalist)},
		{"an empty value", skinCookieName, "", string(view.SkinBrutalist)},
		{"a value nothing declares", skinCookieName, "../../etc/passwd", string(view.SkinBrutalist)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.skinOf(requestWithCookie(tc.cookie, tc.value)); got != tc.want {
				t.Errorf("skinOf() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestA11yOf_IsOffUnlessExplicitlyOn covers the accessibility override.
//
// Strict equality with "on" rather than anything truthy, so a stale or
// partial cookie cannot leave somebody in a mode they did not pick and
// cannot find the control for.
func TestA11yOf_IsOffUnlessExplicitlyOn(t *testing.T) {
	h := &Handler{}

	for _, tc := range []struct {
		name, cookie, value string
		want                bool
	}{
		{"no cookie at all", "", "", false},
		{"explicitly off", a11yCookieName, "off", false},
		{"anything else is not on", a11yCookieName, "true", false},
		{"explicitly on", a11yCookieName, "on", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.a11yOf(requestWithCookie(tc.cookie, tc.value)); got != tc.want {
				t.Errorf("a11yOf() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAppearanceControls_RefuseAMalformedSubmission covers the parse
// failure every one of the three write handlers carries.
//
// It must be a 400 rather than a panic or a silent redirect that wrote
// nothing: a control that appears to work and does not is worse than one
// that reports the problem.
func TestAppearanceControls_RefuseAMalformedSubmission(t *testing.T) {
	p := newAccountProbe(t, nil)

	for _, path := range []string{"/ui/theme", "/ui/skin", "/ui/a11y"} {
		t.Run(path, func(t *testing.T) {
			// "%zz" is an invalid percent escape, which is what
			// url.ParseQuery refuses; a body merely naming no known field
			// parses fine and is a different case.
			//
			// The refusal comes from the preferenceCSRF middleware rather
			// than from the handler, which parses again behind it. That
			// makes each handler's own parse branch unreachable through
			// the router, so this asserts the behaviour a caller actually
			// gets rather than pretending to reach the inner one.
			req := p.authed(http.MethodPost, path, "%zz=1")
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := p.serve(req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("POST %s = %d, want 400 for a body that cannot be parsed; body %s",
					path, rec.Code, rec.Body.String())
			}
			// Nothing may have been written: a control that reports a
			// failure and changes the preference anyway is worse than
			// either outcome alone.
			for _, c := range rec.Result().Cookies() {
				if c.Name == themeCookieName || c.Name == skinCookieName || c.Name == a11yCookieName {
					t.Errorf("POST %s refused the body and still wrote %s", path, c.Name)
				}
			}
		})
	}
}
