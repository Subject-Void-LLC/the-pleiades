// The deployment's own settings, as against a caller's own preferences in
// account.go.
//
// # Why this is a fixed route rather than a registered view
//
// handler.go's route table says every entry exists once for every resource
// that will ever be registered, and a registered view is the declared
// extension point for anything resource-shaped. Settings are not
// resource-shaped: there is one deployment and it has one configuration, so
// there is no collection to list, no identifier to address and no record to
// create. Registering it would mean inventing a singleton record for the
// registry's benefit and then teaching every generic handler to special-case
// it. /account is the existing precedent for a singleton surface and this
// follows it exactly.
//
// # Why the authorization is different from /account's
//
// The account page needs no scope check at all: it carries no identifier, so
// the session IS the subject and there is no field anybody could aim at
// another account. That structural argument does not transfer here, because
// this surface is about everyone rather than about the caller. It is gated on
// auth.ScopeSettingsRead through the same admission chain every other read in
// this UI goes through, so the UI declares no opinion of its own about who may
// see it.
package web

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// showSystemSettings renders the settings area.
func (h *Handler) showSystemSettings(w http.ResponseWriter, r *http.Request) {
	identity := identityFrom(r.Context())
	if !h.permits(r.Context(), identity, auth.ScopeSettingsRead) {
		// Not found rather than forbidden, matching how this UI answers a
		// record in another tenant: telling a caller that a surface they
		// may not see exists is itself a disclosure, and here the surface
		// is the one that would tell them how the deployment authenticates.
		h.notFound(w, r)
		return
	}

	model := view.SystemSettingsModel{
		Page: h.page(r, "Settings", settingsNavName),
		// Unvalidated on purpose: it arrives from the query string, and
		// SystemSettingsModel.CurrentTab is the one place that decides what
		// an unrecognised value means.
		Tab: r.URL.Query().Get("tab"),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := render.SystemSettings(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render system settings", err)
	}
}

// settingsNavName is what marks the navigation entry current.
//
// It is not a registered view name and must never collide with one: the route
// table resolves /{resource} before it would ever reach a view called
// "settings", so a resource registered under that name would be shadowed by
// this page rather than the other way round. The archtest that refuses a
// duplicate view name cannot see this one, which is why it is a named constant
// with this comment rather than a string in two places.
const settingsNavName = "settings"
