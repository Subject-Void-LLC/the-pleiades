// Appearance preferences, and why they are not behind a session.
//
// Skin, theme and accessibility mode set a cookie holding a rendering
// choice. None of them reads a record, writes a record or grants anything,
// which is what makes them the three routes in this UI that a signed-out
// caller may reach.
//
// That is not a convenience. Accessibility mode exists for somebody who
// cannot comfortably read the page in front of them, and the sign-in page is
// a page. Gating the control that makes a page legible behind having already
// read and used that page is a control that arrives one step too late --
// which is exactly what it did, until this file.
package web

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
)

// returnField is the hidden input an appearance form carries to say where it
// was submitted from.
const returnField = "_return"

// maxReturnLength bounds what returnTo will echo into a Location header.
//
// A redirect target is already constrained to this UI's own prefix, so length
// is not a redirection risk; it is a "do not put an unbounded caller-supplied
// string in a response header" risk, which is worth closing on its own.
const maxReturnLength = 2048

// navigationParams are the query parameters that say where in a page the
// reader is, and the only ones a return URL carries.
//
// An allowlist rather than the whole query string, following the rule
// refreshURL already states: a URL this application builds is built from what
// it parsed, never from what it was sent. Without it, a link somebody arrived
// on carrying campaign tags or any other junk would have that junk copied
// into a hidden field on every page and handed back on every preference
// change -- reflected, escaped and harmless, and still not something this
// application should be in the business of echoing.
//
// These four are exactly what this UI's own handlers read: which tab of a
// record is open, which page of a list, how long, and what was searched for.
// A handler that starts reading a fifth belongs in this list, and a reader
// who finds a preference change loses their place is the symptom of it not
// being here.
var navigationParams = []string{"tab", "after", "limit", "q"}

// navigationQuery reduces a query string to the navigation parameters, encoded
// canonically. Empty when none of them are present.
func navigationQuery(in url.Values) string {
	out := url.Values{}
	for _, name := range navigationParams {
		if v := in.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return out.Encode()
}

// optionalSession resolves a session when the request carries one, and lets
// the request through when it does not.
//
// requireSession's twin, and deliberately a separate middleware rather than a
// flag on it. A middleware that sometimes rejects and sometimes does not,
// depending on a boolean set at mount time, is one somebody eventually mounts
// with the wrong boolean on a route that reads records. This one cannot be
// misused that way: it never rejects, so it is only ever correct on a route
// that needs no identity at all.
func (h *Handler) optionalSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), tokenCtxKey, h.cfg.Cookie.Read(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// preferenceCSRF protects a route that may be reached signed in or signed
// out, using whichever proof the caller is in a position to present.
//
// The stateless Sec-Fetch-Site check applies either way and is the layer that
// actually stops classic cross-site posting. On top of it: a signed-in caller
// presents the session-bound token, which is the strong form, and a signed-out
// one presents the double-submit pair the sign-in page already issues. A
// signed-out caller has no session to bind a token to, and minting one to
// create a stronger token here would hand any unauthenticated caller the
// ability to create unbounded session rows -- the same trade login.go records
// and rejects for the same reason.
//
// What is actually at stake if this were bypassed is a cookie holding a
// rendering preference. That is the reason a weaker second layer is
// acceptable here and would not be on a route that changes anything.
func (h *Handler) preferenceCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "malformed form submission", http.StatusBadRequest)
			return
		}
		presented := r.PostFormValue(session.CSRFField)

		// Signed in: the session-bound token, which an attacker who can
		// set cookies on this origin still cannot forge.
		if token := tokenFrom(r.Context()); token != "" {
			sess, err := h.cfg.Sessions.Resolve(r.Context(), token)
			if err == nil {
				if !session.VerifyCSRF(sess.CSRFKey, token, presented) {
					http.Error(w, "invalid CSRF token", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			// A cookie that no longer resolves is a signed-out caller
			// holding a stale cookie, not an attack. Fall through and let
			// them set a preference the way any signed-out caller does.
		}

		// Signed out: the double-submit pair the sign-in page issues.
		cookie, err := r.Cookie(preAuthCookieName)
		if err != nil || !verifyPreAuthToken(cookie.Value, presented) {
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// returnTo is where an appearance control sends the caller back to.
//
// The whole path and query, so a preference changed on a record's Access tab
// returns to that tab and not to the record's first one. That was the bug
// this replaced: safeReturn kept only the path, which was invisible while
// every page's state lived in its path, and started discarding the reader's
// place the moment tabs and cursors moved into the query string. Changing the
// theme from page four of a list put you back on page one.
//
// Two sources, in order. The hidden form field is preferred because this
// server put the caller's current URL into it when it rendered the page, and
// because a Referer is routinely stripped by privacy settings, proxies and
// referrer policies -- a control that loses your place whenever a browser is
// configured tightly is a control that loses your place. The Referer is the
// fallback for a form rendered before this field existed.
//
// Neither source is trusted. Both go through the same constraint: scheme,
// host, userinfo and fragment are discarded rather than inspected, the path
// is cleaned and then required to be inside this UI's own prefix, and what is
// returned is always a rooted path on this origin. Discarding cannot be got
// subtly wrong, whereas "check it looks like one of ours" has a long history
// of being bypassed by a shape nobody thought of. The query rides along
// because a query string cannot change which origin a Location points at.
func (h *Handler) returnTo(r *http.Request) string {
	if candidate := h.constrain(r.PostFormValue(returnField)); candidate != "" {
		return candidate
	}
	if candidate := h.constrain(r.Referer()); candidate != "" {
		return candidate
	}
	return h.cfg.Prefix
}

// constrain reduces a caller-supplied URL to a rooted path plus query inside
// this UI's prefix, or to the empty string when it cannot be.
//
// The one implementation, so the form field and the Referer cannot be
// constrained differently. safeReturn delegates to it too: two functions that
// each decide what counts as safe is how one of them ends up deciding wrong.
func (h *Handler) constrain(raw string) string {
	if raw == "" || len(raw) > maxReturnLength {
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	// Only the path and the query. Everything that could point at another
	// origin is dropped here rather than inspected.
	cleaned := path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
	if cleaned != h.cfg.Prefix && !strings.HasPrefix(cleaned, h.cfg.Prefix+"/") {
		return ""
	}
	query := navigationQuery(parsed.Query())
	if query == "" {
		return cleaned
	}
	// Rebuilt rather than echoed, so what lands in the Location header is a
	// query this package produced from parameters it recognises.
	return cleaned + "?" + query
}

// CurrentURL is where the page being rendered lives, for the hidden field an
// appearance form carries.
//
// Built from this server's own request rather than from anything a caller
// sent, and constrained anyway on the way back in. It is the path and query
// only: a form posting to this origin has no use for the rest.
func (h *Handler) currentURL(r *http.Request) string {
	if r.URL == nil {
		return h.cfg.Prefix
	}
	query := navigationQuery(r.URL.Query())
	if query == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?" + query
}
