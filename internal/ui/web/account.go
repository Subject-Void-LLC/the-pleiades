// Self-service password change.
//
// # Why this is a fixed route with no record id, and not an action on the
// users resource
//
// handler.go's route table says "The table is fixed. Every entry below
// exists once, for every resource that will ever be registered", and record
// actions are the declared extension point for anything resource-shaped. A
// change-password action on `users` would fit that shape and would be
// wrong, for two reasons.
//
// The first is authorization. The users resource is gated on the access
// scopes, and access:write is the scope that "lets a caller decide who else
// may" do things. An ordinary operator changing their OWN password must not
// need the scope that administers everybody else's grants.
//
// The second is stronger, and it is why this route takes no {id}: with no
// identifier in the path there is no way to aim it at somebody else's
// account. The session IS the subject. That makes the authorization
// structural rather than a check inside a handler that a later refactor can
// drop. /logout, /theme, /skin and /a11y are the existing precedent, all
// per-session operations living as fixed routes for the same reason.
//
// Administrative resets stay in the controller subcommand, where Phase 79's
// no-dependency-on-Phase-28 constraint already puts them.
package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/render"
)

// ErrWeakPassword reports a new password refused for what it is, rather
// than a current password refused for being wrong.
//
// The two need different answers and this package cannot import
// internal/localauth to tell them apart (internal/archtest forbids it), so
// the composition root's adapter translates. Comparing on an error message
// would be the alternative, and a message is not an interface.
var ErrWeakPassword = errors.New("web: password refused")

// PasswordChanger replaces a caller's own password, having first proved the
// current one.
//
// Requiring the current password is not ceremony. A session is a bearer
// credential with an eight hour ceiling, so letting one replace the
// password it was minted from would turn a stolen cookie into permanent
// account takeover: the thief changes the password, the owner's own
// credential stops working, and the thief's session continues.
//
// A port rather than a direct dependency on internal/localauth, matching
// PasswordAuthenticator next door.
type PasswordChanger interface {
	ChangePassword(ctx context.Context, subject, oldPassword, newPassword string) error
}

// showAccount renders the account page.
func (h *Handler) showAccount(w http.ResponseWriter, r *http.Request) {
	h.renderAccount(w, r, "", http.StatusOK)
}

// renderAccount draws the account page, optionally with a failure notice.
//
// The notice is a fixed message chosen from a small set rather than an
// error string passed through from below. An error from the credential
// store is written for an operator reading logs and can name why a password
// was refused; this page is read by whoever is sitting at the browser, who
// is not necessarily the account's owner.
func (h *Handler) renderAccount(w http.ResponseWriter, r *http.Request, notice string, status int) {
	page := h.page(r, "Account", "")
	page.Notice = notice

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := render.Account(page).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render account", err)
	}
}

// changePassword replaces the signed-in caller's password.
//
// Every failure re-renders the page rather than redirecting, so the person
// stays where they are. Success redirects, so a refresh does not resubmit.
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	if h.cfg.PasswordChanges == nil {
		// A deployment with no local credentials has nothing to change,
		// and a 404 says exactly that rather than offering a control that
		// can only fail.
		h.notFound(w, r)
		return
	}

	identity := identityFrom(r.Context())
	if identity == nil {
		// requireSession already ran, so this is unreachable in practice.
		// Handled rather than asserted, because the alternative to an
		// explicit refusal is a nil dereference two lines down.
		h.redirectToLogin(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderAccount(w, r, "That form could not be read. Try again.", http.StatusBadRequest)
		return
	}

	var (
		current = r.PostFormValue("current_password")
		next    = r.PostFormValue("new_password")
		confirm = r.PostFormValue("confirm_password")
	)

	// Checked before the store, because it is the one failure that is
	// visibly the person's own and the one the store cannot see: two
	// different new passwords is a typing mistake, not a refused
	// credential.
	if next != confirm {
		h.renderAccount(w, r, "The two new passwords did not match.", http.StatusBadRequest)
		return
	}

	// The subject comes from the SESSION, never from the form. This is the
	// line that makes the route structurally unable to change somebody
	// else's password: there is no field a caller could put an address in.
	err := h.cfg.PasswordChanges.ChangePassword(r.Context(), identity.Subject, current, next)
	switch {
	case err == nil:
		// Fall through to revocation and redirect.
	case errors.Is(err, ErrWeakPassword):
		// The one refusal worth explaining, because the person can act on
		// it, and because "rejected" with no reason leaves an operator
		// guessing at a policy nobody wrote down for them.
		h.renderAccount(w, r, "That new password was refused: "+err.Error(), http.StatusBadRequest)
		return
	default:
		h.cfg.Logger.WarnContext(r.Context(), "password change refused",
			"subject", identity.Subject, "error", err.Error())
		h.renderAccount(w, r, "That did not work. Check your current password and try again.",
			http.StatusUnauthorized)
		return
	}

	// Every OTHER session for this subject is revoked, and this one is
	// kept.
	//
	// Keeping this one is what makes the control usable: signing somebody
	// out of the page they just used, as a reward for improving their own
	// security, teaches them not to do it again. Revoking the rest is the
	// point of the exercise, because the reason to change a password is
	// usually that somebody else may have had it.
	token := tokenFrom(r.Context())
	revoked, err := h.cfg.Sessions.DeleteForSubject(r.Context(), identity.Subject, token)
	if err != nil {
		// Logged loudly and NOT reported as a failed change, because the
		// password really did change. Telling the person it failed would
		// have them try again with what is now the old password.
		h.cfg.Logger.ErrorContext(r.Context(), "password changed but other sessions could not be revoked",
			"subject", identity.Subject, "error", err.Error())
	} else {
		h.cfg.Logger.InfoContext(r.Context(), "password changed",
			"subject", identity.Subject, "sessions_revoked", revoked)
	}

	http.Redirect(w, r, h.cfg.Prefix+"/account", http.StatusSeeOther)
}
