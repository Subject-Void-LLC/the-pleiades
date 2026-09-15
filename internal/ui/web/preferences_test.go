// Package web's appearance-preference tests: that accessibility mode works
// without a session, that it is the first thing a keyboard user reaches when
// there is none, and that changing a preference does not throw away where the
// reader was.
package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestA11y_WorksWithoutASession is the whole point of accessibility mode
// being outside the session gate.
//
// The control exists for somebody who cannot comfortably read the page in
// front of them. The sign-in page is a page. A toggle that only works once
// you have signed in is a toggle that asks that person to first read and
// operate the form they cannot read, which is the wrong way round and is
// exactly what this route did while it sat behind requireSession.
func TestA11y_WorksWithoutASession(t *testing.T) {
	p := newAccountProbe(t, nil)

	// The sign-in page issues the double-submit pair, so a signed-out
	// caller reaches it the way a browser does: render the page, keep the
	// cookie, submit the token it rendered.
	login := p.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil))
	if login.Code != http.StatusOK {
		t.Fatalf("GET /ui/login = %d, want 200", login.Code)
	}
	body := login.Body.String()

	if !strings.Contains(body, "Accessibility mode") {
		t.Fatal("the sign-in page offers no accessibility control")
	}

	token := valueOfInput(body, "_csrf")
	if token == "" {
		t.Fatal("the sign-in page rendered no CSRF token for the accessibility form")
	}

	form := url.Values{"a11y": {"on"}, "_csrf": {token}}
	req := httptest.NewRequest(http.MethodPost, "/ui/a11y", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range login.Result().Cookies() {
		req.AddCookie(c)
	}

	rec := p.serve(req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /ui/a11y signed out = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	// And the preference actually took, rather than the route merely
	// answering politely.
	var set bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == a11yCookieName && c.Value == "on" {
			set = true
		}
	}
	if !set {
		t.Error("the accessibility preference was not recorded for a signed-out caller")
	}
}

// TestA11y_IsTheFirstFocusableThingWhenSignedOut proves the control is
// reachable in one keystroke, not merely present.
//
// Present but twentieth in the tab order is present in the way a fire exit
// behind a locked door is present. On a signed-out page it comes before even
// the skip link: a skip link exists to get past navigation, and there is none
// to get past here.
func TestA11y_IsTheFirstFocusableThingWhenSignedOut(t *testing.T) {
	p := newAccountProbe(t, nil)

	body := p.serve(httptest.NewRequest(http.MethodGet, "/ui/login", nil)).Body.String()

	// Measured from the control's own form, not from its label: the form's
	// hidden CSRF and return inputs sit inside it and ahead of the button,
	// and a hidden input is not a tab stop.
	a11y := strings.Index(body, `class="a11y-bar"`)
	skip := strings.Index(body, "skip-link")
	if a11y < 0 {
		t.Fatal("the sign-in page offers no accessibility control")
	}
	if skip < 0 {
		t.Fatal("the sign-in page has no skip link")
	}
	if a11y > skip {
		t.Error("the accessibility control comes after the skip link, so it is not the first thing a keyboard user reaches")
	}

	// Nothing focusable may precede it. The banner, when configured, is a
	// div; the live announcer is a div. A stray link or button added above
	// it later is what this catches.
	for _, tag := range focusableTags(body[:a11y]) {
		t.Errorf("a focusable element (%s) precedes the accessibility control", tag)
	}
}

// focusableTags names the focusable elements in a fragment of markup.
//
// A hidden input is excluded because it is not a tab stop; every other input
// is, which is why this does not simply skip all of them. Crude on purpose:
// see valueOfInput.
func focusableTags(fragment string) []string {
	var found []string
	for _, tag := range []string{"<a ", "<button", "<select", "<textarea"} {
		if strings.Contains(fragment, tag) {
			found = append(found, tag)
		}
	}
	for i := 0; ; {
		j := strings.Index(fragment[i:], "<input")
		if j < 0 {
			break
		}
		start := i + j
		end := strings.Index(fragment[start:], ">")
		if end < 0 {
			break
		}
		if !strings.Contains(fragment[start:start+end], `type="hidden"`) {
			found = append(found, "<input")
			break
		}
		i = start + end
	}
	return found
}

// TestReturnTo_KeepsTheReaderWhereTheyWere is the settings-behaviour
// property: changing a preference is not navigation.
//
// Somebody on a record's Access tab who switches to dark mode expects to be
// on that record's Access tab, in dark mode. Returning them to the record's
// first tab, or to the list, is the interface deciding it knows better than
// they do where they were going.
func TestReturnTo_KeepsTheReaderWhereTheyWere(t *testing.T) {
	h := &Handler{cfg: Config{Prefix: "/ui"}}

	for _, tc := range []struct {
		name  string
		field string
		ref   string
		want  string
	}{
		{
			name:  "a record's tab survives",
			field: "/ui/jobs/4821?tab=device-outcomes",
			want:  "/ui/jobs/4821?tab=device-outcomes",
		},
		{
			name:  "a list's page survives",
			field: "/ui/jobs?after=4800&limit=25",
			want:  "/ui/jobs?after=4800&limit=25",
		},
		{
			name:  "the settings tab survives, so a preference change does not leave settings",
			field: "/ui/account?tab=password",
			want:  "/ui/account?tab=password",
		},
		{
			// The field is preferred, because a Referer is routinely
			// stripped and this one is not.
			name:  "the form field beats the referer",
			field: "/ui/templates/1?tab=survey",
			ref:   "/ui/jobs",
			want:  "/ui/templates/1?tab=survey",
		},
		{
			name: "the referer is the fallback",
			ref:  "/ui/devices/edge-fra-01?tab=capabilities",
			want: "/ui/devices/edge-fra-01?tab=capabilities",
		},
		{
			// Following refreshURL's rule: built from what was parsed,
			// never from what was sent.
			name:  "an unrecognised parameter is dropped",
			field: "/ui/jobs?tab=details&utm_source=chat",
			want:  "/ui/jobs?tab=details",
		},
		{
			name: "nothing at all falls back to the prefix",
			want: "/ui",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{}
			if tc.field != "" {
				form.Set(returnField, tc.field)
			}
			r := httptest.NewRequest(http.MethodPost, "/ui/theme", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.ref != "" {
				r.Header.Set("Referer", "https://control.example.test"+tc.ref)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}

			if got := h.returnTo(r); got != tc.want {
				t.Errorf("returnTo = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReturnTo_CannotBeRedirectedOffOrigin is the security half, and it
// covers the new input the hidden field introduced.
//
// safeReturn's own test already proves this for the Referer. The form field
// is a second caller-controlled source reaching the same http.Redirect, so it
// gets the same evidence rather than the assumption that it shares a code
// path -- sharing one is exactly the thing a later refactor breaks.
func TestReturnTo_CannotBeRedirectedOffOrigin(t *testing.T) {
	h := &Handler{cfg: Config{Prefix: "/ui"}}

	for _, hostile := range []string{
		"https://evil.example/ui/jobs",
		"//evil.example/ui",
		"https://ui.example.com@evil.example/ui",
		`\\evil.example\ui`,
		"/ui/../../etc/passwd",
		"/admin",
		"ui/jobs",
		"javascript:alert(1)",
		"/ui" + strings.Repeat("/a", maxReturnLength),
	} {
		t.Run(hostile, func(t *testing.T) {
			form := url.Values{returnField: {hostile}}
			r := httptest.NewRequest(http.MethodPost, "/ui/theme", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}

			got := h.returnTo(r)
			if !strings.HasPrefix(got, "/ui") {
				t.Fatalf("returnTo = %q, which is not inside this UI's prefix", got)
			}
			if strings.HasPrefix(got, "//") || strings.Contains(got, "evil.example") {
				t.Fatalf("returnTo = %q, which can leave this origin", got)
			}
		})
	}
}

// valueOfInput reads a hidden input's value out of rendered HTML.
//
// Deliberately crude: this package's tests assert on markup as a browser
// would receive it, and pulling in a parser to read one attribute would make
// the test depend on something the application does not.
func valueOfInput(body, name string) string {
	marker := `name="` + name + `" value="`
	i := strings.Index(body, marker)
	if i < 0 {
		return ""
	}
	rest := body[i+len(marker):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}
