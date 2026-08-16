// The sign-in path: the two credentials it accepts, and the CSRF layer
// that has to be weaker here than everywhere else.
//
// This file exists because doLogin outgrew auth.go once it stopped being a
// token exchange. auth.go keeps the post-authentication machinery (the
// session-bound CSRF middleware, logout, the preference cookies); this
// holds everything that runs before a session exists.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
)

// PasswordAuthenticator proves an email and password belong together and
// returns the subject they prove.
//
// A port rather than a direct dependency on internal/localauth, for the
// same reason Config already takes Tokens as an api.TokenValidator: this
// package renders pages and should not be able to reach a password store
// except through the one narrow verb it needs. It returns a SUBJECT rather
// than an account, because a subject is all a login needs in order to
// derive an identity, and handing a handler anything richer invites it to
// make an authorization decision here instead of in the one place that
// makes them.
//
// Implementations must answer identically for a wrong password, an unknown
// address and a locked account. internal/localauth does; see its package
// doc for why that is a property of the type rather than a rule.
type PasswordAuthenticator interface {
	Authenticate(ctx context.Context, email, password string) (subject string, err error)
}

// IdentityDeriver turns a proven subject into what that subject may do.
//
// This is the step a token login does not need and a password login cannot
// skip: a JWT CARRIES its role and scopes, and an email carries nothing but
// a name. auth.IdentityBuilder is the implementation.
type IdentityDeriver interface {
	Build(ctx context.Context, subject string) (*auth.Identity, error)
}

// preAuthCookieName holds the pre-session CSRF secret.
//
// The __Host- prefix, the same as the session cookie: the browser then
// enforces Secure, Path=/ and no Domain, so no other host under this
// registrable domain can set the value that the double-submit check
// compares against. That prefix was previously dropped whenever the
// deployment had opted out of Secure, which Phase 20 removed by making the
// controller refuse to serve plain HTTP unattended.
const preAuthCookieName = "__Host-pleiades_login"

// preAuthTTL bounds how long a rendered login form stays submittable.
//
// Long enough to type a password, short enough that a form left open in a
// tab overnight does not stay a valid CSRF vehicle.
const preAuthTTL = 30 * time.Minute

// showLogin renders the sign-in page and mints the pre-auth CSRF pair.
func (h *Handler) showLogin(w http.ResponseWriter, r *http.Request) {
	h.renderLogin(w, r, false, http.StatusOK)
}

// doLogin exchanges a credential for a session cookie.
//
// It accepts two credentials and treats them as one decision with two
// proofs. A PASSWORD is proven against the local credential store and then
// turned into an identity by DERIVING role and scopes from the RoleBindings
// on the subject's Teams. A TOKEN is validated through the same
// auth.Evaluator the Bearer path uses, and CARRIES its role and scopes as
// claims. Both then take the identical path below: mint a session, write
// the cookie, redirect. Nothing after the credential check knows which one
// was used, which is what keeps this from becoming two logins.
//
// Both are kept rather than the token path being replaced. A deployment
// federating against an external issuer may hold no local credentials at
// all, and the token field is the documented break-glass route; removing it
// in the change that adds passwords would take away the only way in for
// exactly the deployments that have not adopted the new way yet. Which of
// the two a deployment offers is a configuration question: password login
// is refused when no PasswordAuthenticator is wired, and token login is
// refused when no validator is.
func (h *Handler) doLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderLogin(w, r, true, http.StatusBadRequest)
		return
	}

	var (
		email    = strings.TrimSpace(r.PostFormValue("email"))
		password = r.PostFormValue("password")
		token    = strings.TrimSpace(r.PostFormValue("token"))
	)

	var identity *auth.Identity
	switch {
	case email != "" || password != "":
		identity = h.passwordLogin(r, email, password)
	case token != "":
		identity = h.tokenLogin(r, token)
	}

	if identity == nil {
		// One response for every failure, of either kind. Distinguishing
		// them would say which credentials a deployment even accepts, and
		// within the password path it would say which accounts exist.
		h.renderLogin(w, r, true, http.StatusUnauthorized)
		return
	}

	// Minted only after a credential succeeded, so there is no pre-auth
	// session to fixate.
	sessionToken, err := h.cfg.Sessions.Create(r.Context(), identity,
		session.DefaultIdleTimeout, session.DefaultAbsoluteTimeout)
	if err != nil {
		h.serverError(w, r, "create session", err)
		return
	}

	// The pre-auth CSRF cookie has done its job and is replaced by the
	// session-bound one. Leaving it set would be a second, weaker CSRF
	// secret alive alongside the strong one.
	h.clearPreAuthCookie(w)
	h.cfg.Cookie.Write(w, sessionToken)
	http.Redirect(w, r, h.cfg.Prefix, http.StatusSeeOther)
}

// passwordLogin proves a local credential and derives what it may do.
//
// Returns nil for every failure. The reason is logged by the store, which
// is the only place that can tell a wrong password from an unknown account
// without handing that distinction to the caller.
func (h *Handler) passwordLogin(r *http.Request, email, password string) *auth.Identity {
	if h.cfg.Passwords == nil || h.cfg.Identities == nil {
		h.cfg.Logger.WarnContext(r.Context(), "password login attempted but not configured",
			"hint", "wire a PasswordAuthenticator and an IdentityDeriver, or remove the fields from the sign-in form")
		return nil
	}

	subject, err := h.cfg.Passwords.Authenticate(r.Context(), email, password)
	if err != nil {
		return nil
	}

	// The derivation, not the authentication. A failure here means the
	// person is who they say they are and this deployment could not work
	// out what they may do, which is a server problem rather than a
	// credential problem, so it is logged loudly rather than silently
	// counted as a bad password.
	identity, err := h.cfg.Identities.Build(r.Context(), subject)
	if err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "failed to derive an identity for an authenticated subject",
			"subject", subject, "error", err.Error())
		return nil
	}
	return identity
}

// tokenLogin validates a pasted token, unchanged from what Phase 19 built.
func (h *Handler) tokenLogin(r *http.Request, token string) *auth.Identity {
	if h.cfg.Tokens == nil {
		return nil
	}
	identity, err := h.cfg.Tokens.ValidateToken(r.Context(), token)
	if err != nil || identity == nil {
		return nil
	}
	return identity
}

// DefaultLoginRateLimiterConfig throttles sign-in attempts per source.
//
// One attempt every two seconds with a burst of five, against the versioned
// API's default of fifty per second. The gap is the point: fifty per second
// is sized for a CI pipeline retrying a dispatch call, and applying it to a
// login form would permit millions of guesses a day from one address while
// technically being "rate limited".
//
// This bounds RATE and keys on the SOURCE. The credential store's lockout
// bounds ATTEMPTS and keys on the ACCOUNT, and the derivation gate bounds
// MEMORY. Three mechanisms, three keys, and this one is the weakest of the
// three against a determined attacker precisely because a source address is
// the cheapest of the three things to vary.
var DefaultLoginRateLimiterConfig = api.RateLimiterConfig{
	RequestsPerSecond: 0.5,
	Burst:             5,
}

// loginRateLimit sheds sign-in attempts from a source that is trying too
// often.
//
// It CONSUMES api.RateLimiter rather than reimplementing a bucket, and it
// deliberately does not reuse api.RateLimitMiddleware: that middleware
// answers with RespondError, which writes a JSON body, and the only client
// of this route is a browser rendering HTML. The token bucket, the eviction
// policy and the Retry-After arithmetic are all the shared implementation's;
// only the refusal's content type differs.
//
// Nil limiter means no throttling, which is a real configuration for a
// single-operator deployment and is why the field is optional rather than
// defaulted into existence.
func (h *Handler) loginRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.LoginLimiter == nil || h.cfg.LoginLimiter.Allow(loginCallerKey(r)) {
			next.ServeHTTP(w, r)
			return
		}
		// 429 with the form re-rendered, rather than a bare error page, so
		// a legitimate operator who typed too fast can simply try again.
		h.renderLogin(w, r, true, http.StatusTooManyRequests)
	})
}

// loginCallerKey bills a sign-in attempt to its source address.
//
// It mirrors internal/api's own callerKey address branch, including the
// part that looks like an omission and is not: X-Forwarded-For and
// X-Real-IP are never read. A header a client can set is a rate limit a
// client can evade, so trusting one requires knowing which proxy is in
// front of this process, and nothing here knows that.
//
// The consequence is worth stating rather than leaving to be discovered:
// behind an ingress that does not preserve the source address, every
// browser shares one bucket, which makes this limiter useless there and
// mildly harmful. Phase 19 recorded that boundary as open and assigned it
// to the deployment work that owns the ingress. That work has now happened
// (Phase 20, TLS termination), and it did not change this line, which is
// the outcome worth recording rather than a task still outstanding: the
// answer to "which proxy is in front of this process" turned out to be an
// explicit deployment setting, not a header this code should start
// believing. An operator who wants per-source limiting behind a proxy has
// to make the proxy preserve the real source address at the connection
// level; there is no header this process can trust to do it for them. The
// per-account lockout is the mechanism that still works either way, which
// is part of why it is not optional.
//
// There is deliberately no email in this key. Keying a limiter on the
// submitted address would make it trivially evadable (vary the address) and
// would duplicate what lockout already does properly.
func loginCallerKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "login:" + r.RemoteAddr
	}
	return "login:" + host
}

// preAuthCSRF refuses a sign-in attempt that did not come from a sign-in
// page this server rendered.
//
// # This is weaker than the post-authentication posture, and the gap is
// unavoidable rather than an oversight
//
// The csrf middleware in auth.go rests its strongest claim on a per-session
// server-held HMAC key: an attacker who can set cookies on this origin
// still cannot forge a token, because they cannot read the key. Before a
// login there is no session and no key, so that claim cannot be made here.
// What is left is a double-submit pair (a cookie and a form field that must
// agree) plus the stateless Sec-Fetch-Site check, which is the same first
// layer the post-auth middleware uses.
//
// The rejected alternative is worth recording. Minting a real session row
// to hold a pre-auth CSRF key would restore the strong property, and it
// would let any unauthenticated caller create unbounded rows, and it would
// reverse doLogin's own "minted only after a credential succeeded, so there
// is no pre-auth session to fixate". A weaker CSRF layer is a better trade
// than a session-fixation surface plus a denial of service.
//
// What this actually defends: login CSRF, where an attacker forces a
// victim's browser to sign in as the ATTACKER, so the victim then works
// inside an account the attacker controls and reads. That was low value
// when the only credential was a token the attacker had to already possess.
// It is worth more now that a password login exists.
func (h *Handler) preAuthCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		// Layer one, stateless: a browser tells us where the request came
		// from, and no cross-site context has any business posting here.
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}

		// Layer two, double submit: the cookie was set when this server
		// rendered the form, and a cross-origin attacker cannot read it to
		// copy its value into their own form.
		cookie, err := r.Cookie(preAuthCookieName)
		if err != nil {
			h.renderLogin(w, r, true, http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			h.renderLogin(w, r, true, http.StatusBadRequest)
			return
		}
		presented := r.PostFormValue(session.CSRFField)
		if !verifyPreAuthToken(cookie.Value, presented) {
			h.renderLogin(w, r, true, http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// issuePreAuthCookie mints a fresh CSRF secret and returns the form-field
// value that must accompany it.
//
// The cookie holds a random secret; the form carries an HMAC of it. Storing
// the same value in both would still work as a double submit, but this way
// the value in the HTML is not itself the cookie, so an HTML injection that
// leaks the page does not hand over a cookie an attacker can then set.
func (h *Handler) issuePreAuthCookie(w http.ResponseWriter) string {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		// Fail closed by issuing nothing. The form will then be refused,
		// which is the correct outcome: a CSRF token from a degraded
		// entropy source is worse than an unusable form, because it looks
		// like protection.
		return ""
	}
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	h.writePreAuthCookie(w, encoded, int(preAuthTTL.Seconds()))
	return preAuthToken(encoded)
}

// clearPreAuthCookie removes the pre-auth secret once a session exists.
func (h *Handler) clearPreAuthCookie(w http.ResponseWriter) {
	h.writePreAuthCookie(w, "", -1)
}

// writePreAuthCookie sets or clears the pre-auth cookie.
//
// SameSite is Strict, unlike the appearance preferences: a cookie whose
// only job is to prove a form came from this origin has no business
// travelling with a cross-site navigation, and it is the first of the two
// double-submit halves.
//
// One writer, one literal. There used to be a second one that dropped
// Secure so the login form worked over plain HTTP, and it carried a gosec
// waiver saying so. What it cost was worth more than it looked: over plain
// HTTP a network attacker can read this cookie and forge the matching form
// field, so the double-submit layer was worth materially less and login
// CSRF rested on SameSite=Strict and Sec-Fetch-Site alone. Phase 20 removed
// the mode rather than the layer, by making the controller refuse to serve
// plain HTTP unattended.
func (h *Handler) writePreAuthCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     preAuthCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// preAuthKey is the process-wide key the pre-auth HMAC is taken under.
//
// Process-wide rather than per-session is exactly what makes this layer
// weaker than the post-auth one, and it is unavoidable: there is no session
// to key on yet. It is still not derivable by an attacker, so it prevents a
// third party from computing a matching pair for a cookie they cannot read.
//
// A restart invalidates every outstanding login form, which is acceptable:
// the failure mode is one refused sign-in and a re-render, not a lost
// session, because sessions are keyed on the database rather than on this.
var preAuthKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		// Unreachable in practice. A fixed fallback keeps the double-submit
		// comparison working rather than failing every login open.
		copy(key, "pleiades pre-auth csrf fallback")
	}
	return key
}()

// preAuthToken derives the form-field value for a cookie value.
func preAuthToken(cookieValue string) string {
	mac := hmac.New(sha256.New, preAuthKey)
	mac.Write([]byte(cookieValue))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyPreAuthToken reports whether presented matches cookieValue, in
// constant time.
func verifyPreAuthToken(cookieValue, presented string) bool {
	if cookieValue == "" || presented == "" {
		return false
	}
	want := preAuthToken(cookieValue)
	return subtle.ConstantTimeCompare([]byte(want), []byte(presented)) == 1
}
