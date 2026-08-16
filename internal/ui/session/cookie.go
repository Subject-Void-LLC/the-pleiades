package session

import (
	"net/http"
	"time"
)

// Cookie names. The __Host- prefix is not decoration: it is a rule the
// browser enforces, requiring Secure, Path=/, and no Domain attribute, and
// refusing the cookie outright if any of those is missing.
//
// The no-Domain half is what matters most here. Without it, a compromised
// or attacker-controlled subdomain can set a cookie the parent origin will
// send, which is precisely the attack that defeats naive double-submit
// CSRF. Enforcing it in the browser rather than trusting the server to set
// the attributes correctly forever is the difference between a guarantee
// and a convention.
const (
	SecureCookieName = "__Host-pleiades_session"

	// CSRFHeader is the header HTMX requests carry the CSRF token in, and
	// CSRFField is the hidden form input non-JavaScript submissions use.
	// Both exist so the protection works with and without scripting.
	CSRFHeader = "X-CSRF-Token"
	CSRFField  = "_csrf"
)

// CookieCodec reads and writes the session cookie.
//
// It holds no configuration, and that emptiness is the point rather than an
// oversight: it used to carry an Insecure flag that dropped the __Host-
// prefix and the Secure attribute for a developer running the controller
// over plain HTTP. Phase 20 deleted that flag by making the controller
// refuse to serve plain HTTP unattended (cmd/controller's resolveTLS), so
// there is no longer any configuration under which this codec should write
// a cookie a browser can send in the clear.
//
// The type survives its own field on purpose. Two things construct it, the
// UI subtree and the JSON API's cookie credential source, and a package
// function instead of a value would make it possible for those two to reach
// different code some day; one value passed to both is what guarantees a
// session written under one name is read under the same one.
type CookieCodec struct{}

// Name returns the cookie name this codec reads and writes.
//
// A constant now. It used to branch on the deleted Insecure flag, which is
// why this is a method on a value with no fields rather than a bare
// constant: every caller already holds the codec, and collapsing the method
// away would churn the same call sites again if a second name ever returns.
func (c CookieCodec) Name() string {
	return SecureCookieName
}

// Write sets the session cookie on w.
//
// There is deliberately no Expires or Max-Age. The cookie is a session
// cookie, discarded when the browser closes, and the server owns the real
// deadlines: a Max-Age would be a second, client-held copy of an expiry
// the database already tracks, and the two would disagree the moment a
// session was revoked early.
//
// # Why this file used to hold two nearly identical cookie literals
//
// Worth recording, because the shape that replaced them looks like it was
// always this simple. Write and Clear each had a second, insecure twin,
// and the duplication was a deliberate concession to static analysis:
// gosec's G124 reads the literal attributes at the call site, so
// `Secure: !c.Insecure` was unprovable to it and one shared literal would
// have put a waiver on the DEFAULT, secure path, which is precisely where
// a waiver must never sit. Split, the normal path was provably correct and
// the one flagged call was the deliberate opt-out.
//
// The mistake not to repeat, since it looks like an obvious cleanup and
// was tried and reverted in Phase 79b: consolidating this writer with the
// appearance-preference and pre-auth CSRF writers in internal/ui/web. Those
// three cookies need different SameSite values, so a shared writer takes
// SameSite as a PARAMETER, and a parameter is exactly as unprovable to
// gosec as a computed Secure field. Consolidating therefore did not remove
// a waiver, it added one on the production path. Both problems are gone now
// for the same reason: with no insecure mode left, every one of these
// writers states Secure as a literal true, so there is nothing to prove and
// nothing to waive.
func (c CookieCodec) Write(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:  SecureCookieName,
		Value: token,
		Path:  "/",
		// HttpOnly keeps the token out of reach of every script on the
		// page, which is the whole reason this is not a token in local
		// storage.
		HttpOnly: true,
		Secure:   true,
		// Strict, not Lax. Lax would attach the cookie to top-level
		// navigations from other sites, which is convenient for
		// "click a link, arrive logged in" and is exactly the vector a
		// state-changing GET would be exploited through. This UI has no
		// cross-site entry point worth that trade.
		SameSite: http.SameSiteStrictMode,
	})
}

// Clear expires the session cookie on w, for logout.
func (c CookieCodec) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SecureCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		// A past expiry and a zero Max-Age together, because browsers
		// have historically disagreed about which one deletes a cookie.
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
}

// Read returns the session token r carries, or the empty string.
func (c CookieCodec) Read(r *http.Request) string {
	cookie, err := r.Cookie(c.Name())
	if err != nil {
		return ""
	}
	return cookie.Value
}
