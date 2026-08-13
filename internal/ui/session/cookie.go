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
	SecureCookieName   = "__Host-pleiades_session"
	InsecureCookieName = "pleiades_session"

	// CSRFHeader is the header HTMX requests carry the CSRF token in, and
	// CSRFField is the hidden form input non-JavaScript submissions use.
	// Both exist so the protection works with and without scripting.
	CSRFHeader = "X-CSRF-Token"
	CSRFField  = "_csrf"
)

// CookieCodec reads and writes the session cookie.
//
// Insecure exists because __Host- requires Secure, Secure requires HTTPS,
// and a developer running the controller on http://localhost would
// otherwise be unable to log in at all. It is a stated choice rather than
// a default the code falls into when a field is left zero -- the same
// posture api.RouterConfig.AllowUnauthenticated already takes -- and the
// composition root logs a warning whenever it is on.
type CookieCodec struct {
	// Insecure drops the __Host- prefix and the Secure attribute. Never
	// set it in a deployment reachable by anyone but the developer who
	// set it.
	Insecure bool
}

// Name returns the cookie name this codec reads and writes.
func (c CookieCodec) Name() string {
	if c.Insecure {
		return InsecureCookieName
	}
	return SecureCookieName
}

// Write sets the session cookie on w.
//
// There is deliberately no Expires or Max-Age. The cookie is a session
// cookie, discarded when the browser closes, and the server owns the real
// deadlines: a Max-Age would be a second, client-held copy of an expiry
// the database already tracks, and the two would disagree the moment a
// session was revoked early.
// The secure and insecure paths are written as two separate literals
// rather than one struct with a computed Secure field, and that is a
// deliberate concession to static analysis rather than duplication for its
// own sake. gosec's G124 checks the literal attributes at the call site;
// `Secure: !c.Insecure` is unprovable to it, so a single shared literal
// would put a waiver on the *default*, secure path -- which is precisely
// where a waiver must never sit. Split this way, the normal path is
// provably correct to the scanner and the one flagged call is the
// deliberate development-only opt-out, where a waiver states something
// true and specific.
func (c CookieCodec) Write(w http.ResponseWriter, token string) {
	if c.Insecure {
		c.writeInsecure(w, token, 0, time.Time{})
		return
	}
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
	if c.Insecure {
		c.writeInsecure(w, "", -1, time.Unix(0, 0))
		return
	}
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

// writeInsecure is the development-only path: no __Host- prefix and no
// Secure attribute, because both require HTTPS and a developer running the
// controller on http://localhost has none. Everything that does not depend
// on TLS -- HttpOnly, SameSite=Strict, Path=/ -- is retained, so this is a
// narrow concession rather than an unprotected cookie.
func (c CookieCodec) writeInsecure(w http.ResponseWriter, value string, maxAge int, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     InsecureCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   maxAge,
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
