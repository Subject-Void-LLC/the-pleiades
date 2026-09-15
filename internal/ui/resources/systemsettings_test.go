// Package resources_test's settings-area tests, and in particular its
// authorization.
//
// The settings area is a fixed route rather than a registered view, so the
// conformance suite that walks every descriptor never sees it: the scope that
// guards it has to be asserted here or nowhere.
package resources_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// The settings area is a fixed route rather than a registered view, which
// means the conformance suite that walks every descriptor does not see it at
// all. Its authorization therefore has to be asserted here, directly, because
// nothing else in this package will ever ask the question for it.

// TestSystemSettings_IsGatedOnItsOwnScope is the assertion that matters most
// on this surface.
//
// The page names an LDAP bind account, an aggregator endpoint and the base URL
// every identity-provider callback is built from. That is a map of the estate
// even with every secret redacted, which is why settings:read is its own scope
// and not something acquired as a side effect of administering role bindings.
func TestSystemSettings_IsGatedOnItsOwnScope(t *testing.T) {
	// The viewer holds inventory:read, job:read, runbook:read and
	// template:read, and deliberately not settings:read. Reading the fleet
	// must not carry the ability to read how the deployment authenticates.
	//
	// The conformance ADMIN is not the right identity for this assertion and
	// is worth saying why: auth.Identity.HasScope lets RoleAdmin bypass every
	// scope check unconditionally, so an admin reaches this page whatever
	// scopes it holds. That is the platform's own rule rather than this
	// surface's, and it is a real property to know about a page that names an
	// LDAP bind account and the base URL every callback is built from: role
	// admin is settings access, today, with no separate grant.
	h := newHarness(t, viewerIdentity)
	rec := h.get(t, "/ui/settings")
	if rec.Code != http.StatusNotFound {
		t.Errorf("a caller without settings:read got %d, want 404", rec.Code)
	}

	// Not found rather than forbidden. Telling a caller that a surface they
	// may not see exists is a disclosure, and this is the surface that would
	// tell them how the deployment authenticates.
	if strings.Contains(rec.Body.String(), "LDAP") {
		t.Error("the settings page leaked its content to a caller who may not read it")
	}

	// And the navigation does not advertise it either, which is the failure
	// a second opinion about authorization always produces: a link somebody
	// follows into a 404.
	if strings.Contains(h.get(t, "/ui/jobs").Body.String(), `href="/ui/settings"`) {
		t.Error("the sidebar offered a settings link to a caller the route refuses")
	}
}

// TestSystemSettings_RendersEveryTileToAPermittedCaller is the other half: the
// gate above is only meaningful if the page exists for somebody.
func TestSystemSettings_RendersEveryTileToAPermittedCaller(t *testing.T) {
	h := newHarness(t, settingsAdminIdentity)

	rec := h.get(t, "/ui/settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ui/settings with settings:read = %d, want 200", rec.Code)
	}
	// This caller is an operator, not an admin, so reaching the page is
	// evidence about the scope rather than about the admin bypass.
	body := rec.Body.String()

	// Every tile is offered, and the one a bare URL opens has rendered.
	for _, tile := range (view.SystemSettingsModel{}).Tiles() {
		if !strings.Contains(body, tile.Title) {
			t.Errorf("the settings page offers no %q tile", tile.Title)
		}
	}
	if !strings.Contains(body, "LDAP") {
		t.Error("the first tile did not render its groups")
	}

	// It says out loud that nothing here works, which is the difference
	// between a declared surface and a broken one.
	if !strings.Contains(body, "Declared, not implemented") {
		t.Error("the settings page does not say it is declared")
	}

	// Nothing on it is submittable. A control that looks ready to accept a
	// client secret and then discards it is worse than no control.
	if strings.Contains(body, `<form`) && strings.Contains(body, `name="ldap_bind_password"`) {
		t.Error("the settings page rendered a live control for a value it cannot store")
	}

	// And the navigation offers it, since this caller may reach it.
	if !strings.Contains(h.get(t, "/ui/jobs").Body.String(), `href="/ui/settings"`) {
		t.Error("the sidebar does not offer settings to a caller who may read them")
	}
}

// TestSystemSettings_EachTileIsReachable proves the tab strip links somewhere,
// rather than merely rendering the right words.
func TestSystemSettings_EachTileIsReachable(t *testing.T) {
	h := newHarness(t, settingsAdminIdentity)

	for _, tile := range (view.SystemSettingsModel{}).Tiles() {
		t.Run(tile.Slug(), func(t *testing.T) {
			body := h.get(t, "/ui/settings?tab="+tile.Slug()).Body.String()
			for _, g := range tile.Groups {
				if !strings.Contains(body, g.Title) {
					t.Errorf("following the %q tab did not render its %q group", tile.Title, g.Title)
				}
			}
		})
	}

	// An unrecognised tab renders the first tile rather than a blank page.
	body := h.get(t, "/ui/settings?tab=no-such-tile").Body.String()
	if !strings.Contains(body, "LDAP") {
		t.Error("an unrecognised tab did not fall back to the first tile")
	}
}
