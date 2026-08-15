package web

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// themeCookieName carries an anonymous visitor's appearance choice.
//
// A logged-in user's preference could live on their session row, but the
// login page itself has no session and still has to render in the right
// theme, so the cookie is the one mechanism that works everywhere. It
// holds a preference, not a credential, which is why it is readable by
// script and survives logout.
const (
	themeCookieName = "pleiades_theme"
	skinCookieName  = "pleiades_skin"
	a11yCookieName  = "pleiades_a11y"

	// preferenceMaxAge keeps an appearance choice for a year. It is
	// deliberately long: a user who picked high-contrast mode should not
	// have to pick it again next month.
	preferenceMaxAge = 60 * 60 * 24 * 365
)

// themeOf resolves the appearance override for a request.
func (h *Handler) themeOf(r *http.Request) string {
	c, err := r.Cookie(themeCookieName)
	if err != nil {
		return string(view.ThemeSystem)
	}
	return string(view.ParseTheme(c.Value))
}

// skinOf resolves the theme family for a request.
func (h *Handler) skinOf(r *http.Request) string {
	c, err := r.Cookie(skinCookieName)
	if err != nil {
		return string(view.SkinBrutalist)
	}
	return string(view.ParseSkin(c.Value))
}

// a11yOf resolves the explicit accessibility override.
func (h *Handler) a11yOf(r *http.Request) bool {
	c, err := r.Cookie(a11yCookieName)
	return err == nil && c.Value == "on"
}

// writePreference stores one appearance choice.
//
// These carry preferences, not credentials, which is why they are readable
// by script and survive logout: a user who signs out and back in should not
// have to re-choose how the application looks. They are deliberately not on
// the session row -- the login page has no session and still has to render
// correctly.
func (h *Handler) writePreference(w http.ResponseWriter, name, value string) {
	if h.cfg.Cookie.Insecure {
		h.writeInsecurePreference(w, name, value)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:  name,
		Value: value,
		Path:  "/",
		// HttpOnly even though these are preferences rather than
		// credentials. Nothing in this application reads them from
		// script -- the server resolves all three axes and renders them
		// as attributes -- so there is no reason to leave them reachable.
		HttpOnly: true,
		Secure:   true,
		// Lax rather than Strict: arriving from an external link should
		// still render in the appearance the user chose. A preference is
		// not a capability, so the Strict posture the session cookie
		// needs would cost something here and protect nothing.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   preferenceMaxAge,
	})
}

// writeInsecurePreference is the development-only path, split out for the
// same reason CookieCodec's is: the Secure attribute has to be a literal
// for a static analyser to prove the default correct, so the deliberate
// opt-out is isolated here rather than sharing a computed field with the
// production path.
//
// Attempting to share one writer with the pre-auth CSRF cookie was tried
// and reverted, and the reason is worth recording because it looks like an
// obvious cleanup. The two cookies need different SameSite values, so a
// shared writer takes SameSite as a PARAMETER, and a parameter is exactly
// as unprovable to gosec as a computed Secure field is. Consolidating
// therefore does not remove a waiver, it adds one: the production path
// starts getting flagged too, which is the one place a waiver must never
// sit. Duplicated literals are the cost of a scanner that can prove the
// default correct, and that trade is the right way round.
func (h *Handler) writeInsecurePreference(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   preferenceMaxAge,
	})
}

// setSkin records a theme-family choice.
func (h *Handler) setSkin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return
	}
	h.writePreference(w, skinCookieName, string(view.ParseSkin(r.PostFormValue("skin"))))
	http.Redirect(w, r, h.safeReturn(r), http.StatusSeeOther)
}

// setA11y toggles the explicit accessibility override.
//
// It is a real, prominent control rather than a hidden preference, because
// a mode nobody can find is a mode nobody uses. It is never a degraded
// mode: every feature, column and action stays exactly where it was.
func (h *Handler) setA11y(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return
	}
	value := "off"
	if r.PostFormValue("a11y") == "on" {
		value = "on"
	}
	h.writePreference(w, a11yCookieName, value)
	http.Redirect(w, r, h.safeReturn(r), http.StatusSeeOther)
}

// setTheme records an appearance choice and returns where the user was.
//
// It is a real form post rather than a script-driven toggle, so it works
// with JavaScript disabled and needs no client-side persistence. The
// redirect goes back to the referring page, validated to this UI's own
// prefix: a Location built from an unvalidated Referer is an open redirect.
func (h *Handler) setTheme(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return
	}

	h.writePreference(w, themeCookieName, string(view.ParseTheme(r.PostFormValue("theme"))))
	http.Redirect(w, r, h.safeReturn(r), http.StatusSeeOther)
}

// safeReturn is where an appearance control returns the user to.
//
// It discards rather than validates. The Referer is caller-controlled, so
// the scheme, host, userinfo and fragment are thrown away outright and only
// the path survives -- discarding cannot be got subtly wrong, whereas
// "check it looks like one of ours" has a long history of being bypassed by
// a shape nobody thought of (//evil.com, https://ui.example.com@evil.com,
// backslashes some browsers normalise to slashes).
//
// What remains is cleaned, which resolves any "..", and then required to be
// inside this UI's own prefix. The result is always a rooted path on this
// origin, so it cannot be an open redirect regardless of what arrives.
func (h *Handler) safeReturn(r *http.Request) string {
	ref := r.Referer()
	if ref == "" {
		return h.cfg.Prefix
	}

	parsed, err := url.Parse(ref)
	if err != nil {
		return h.cfg.Prefix
	}

	// Only the path. Everything that could point at another origin is
	// dropped here rather than inspected.
	cleaned := path.Clean("/" + strings.TrimPrefix(parsed.Path, "/"))
	if cleaned == h.cfg.Prefix || strings.HasPrefix(cleaned, h.cfg.Prefix+"/") {
		return cleaned
	}
	return h.cfg.Prefix
}

func (h *Handler) renderLogin(w http.ResponseWriter, r *http.Request, failed bool, status int) {
	page := view.PageModel{
		// The banner renders before authentication too: somebody signing
		// in to production should know that before they authenticate.
		Banner:  h.cfg.Banner,
		Title:   "Sign in",
		Theme:   h.themeOf(r),
		Skin:    h.skinOf(r),
		A11y:    h.a11yOf(r),
		Version: h.cfg.Version,
		Prefix:  h.cfg.Prefix,
		// A FRESH pre-auth CSRF pair on every render, including on a failed
		// attempt. Reusing the previous one after a refusal would leave a
		// token valid across an unbounded number of retries, and a re-render
		// is exactly the moment a new one is free to issue. The cookie is
		// set here rather than in the middleware because this is the only
		// place that emits the form the token has to travel with.
		CSRFToken: h.issuePreAuthCookie(w),
		// PasswordLogin drives which fields the form offers. A deployment
		// with no local credential store gets the token field alone rather
		// than a password box that can only ever fail.
		PasswordLogin: h.cfg.Passwords != nil && h.cfg.Identities != nil,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := render.Login(page, failed).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render login", err)
	}
}

// doLogout revokes the session and clears the cookie.
//
// The row is deleted rather than flagged, so the credential stops working
// immediately everywhere rather than when a cache notices. That
// revocability is the whole reason this UI holds a session at all instead
// of a signed token.
func (h *Handler) doLogout(w http.ResponseWriter, r *http.Request) {
	if token := tokenFrom(r.Context()); token != "" {
		if err := h.cfg.Sessions.Delete(r.Context(), token); err != nil {
			h.cfg.Logger.WarnContext(r.Context(), "failed to delete session on logout",
				"error", err.Error())
		}
	}
	h.cfg.Cookie.Clear(w)
	http.Redirect(w, r, path.Join(h.cfg.Prefix, "login"), http.StatusSeeOther)
}

// csrfTokenFor derives this request's CSRF token from the session.
func (h *Handler) csrfTokenFor(r *http.Request) string {
	token := tokenFrom(r.Context())
	if token == "" {
		return ""
	}
	sess, err := h.cfg.Sessions.Resolve(r.Context(), token)
	if err != nil {
		return ""
	}
	return session.CSRFToken(sess.CSRFKey, token)
}

// csrf refuses a state-changing request that does not prove it came from
// this application.
//
// Three independent layers, and all three are kept. SameSite=Strict on the
// session cookie means the browser never attaches it cross-site, which
// alone stops classic CSRF. The Sec-Fetch-Site check is stateless and
// strictly stronger than a token against several bypass classes. The
// session-bound token closes what the other two cannot: because the HMAC
// key is per-session and server-held, an attacker who can set cookies on
// this origin still cannot forge one.
func (h *Handler) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}

		token := tokenFrom(r.Context())
		sess, err := h.cfg.Sessions.Resolve(r.Context(), token)
		if err != nil {
			h.redirectToLogin(w, r)
			return
		}

		presented := r.Header.Get(session.CSRFHeader)
		if presented == "" {
			// ParseForm is safe to call before the handler does: it
			// caches, so the handler's own call is a no-op rather than a
			// second read of a consumed body.
			if err := r.ParseForm(); err == nil {
				presented = r.PostFormValue(session.CSRFField)
			}
		}

		if !session.VerifyCSRF(sess.CSRFKey, token, presented) {
			// No state change has happened at this point, which is what
			// the test asserts rather than merely checking the status.
			http.Error(w, "invalid CSRF token", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}
