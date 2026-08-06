package api_test

import (
	"net/http"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
)

// noopHandler exists only to have something to point a Route at; none of
// this file's tests reach it.
func noopHandler(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }

// TestNewRouter_RejectsNilAuthWithoutOptOut proves an unauthenticated API
// subtree can only happen because a caller said so explicitly, not
// because Auth was left zero by omission. This is the construction-time
// half of Phase 12's own finding: leaving a config field reachable by
// omission is how a real deployment ends up authenticating nothing.
func TestNewRouter_RejectsNilAuthWithoutOptOut(t *testing.T) {
	if _, err := api.NewRouter(api.RouterConfig{}); err == nil {
		t.Error("NewRouter with a nil Auth and no AllowUnauthenticated opt-out succeeded, want an error")
	}
}

// TestNewRouter_AllowUnauthenticatedOptOutSucceeds is the positive
// control for the test above: the explicit opt-out, not the absence of
// Auth alone, is what makes construction succeed.
func TestNewRouter_AllowUnauthenticatedOptOutSucceeds(t *testing.T) {
	if _, err := api.NewRouter(api.RouterConfig{AllowUnauthenticated: true}); err != nil {
		t.Errorf("NewRouter with AllowUnauthenticated: true failed: %v", err)
	}
}

// TestNewRouter_RejectsRouteWithEmptyScope proves a Route cannot be
// registered without declaring the auth.Scope it requires. Without this,
// an empty Scope would silently mean "no scope required," the exact
// authorization gap FAILURE_PATTERNS.md #65 and #66 both trace back to.
func TestNewRouter_RejectsRouteWithEmptyScope(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Admission:            &fakeAdmitter{},
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Handler: noopHandler},
		},
	})
	if err == nil {
		t.Error("NewRouter with an empty Route.Scope succeeded, want an error")
	}
}

// TestNewRouter_RejectsRoutesWithNilAdmission proves a route table cannot
// exist with nothing able to enforce the scopes it declares. A Routes
// slice with a nil Admission is the same silent gap as an empty Scope,
// just moved one field over.
func TestNewRouter_RejectsRoutesWithNilAdmission(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Handler: noopHandler},
		},
	})
	if err == nil {
		t.Error("NewRouter with Routes set and Admission nil succeeded, want an error")
	}
}

// TestNewRouter_RejectsDuplicateRoute proves two Routes cannot register
// the same Method and Pattern. chi itself would let the second silently
// shadow the first; NewRouter refuses to build rather than serve a route
// table where one entry's declared Scope is not actually the Scope
// enforced at that path.
func TestNewRouter_RejectsDuplicateRoute(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Admission:            &fakeAdmitter{},
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Handler: noopHandler},
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeRunbookExecute, Handler: noopHandler},
		},
	})
	if err == nil {
		t.Error("NewRouter with two Routes on the same Method and Pattern succeeded, want an error")
	}
}

// TestNewRouter_EmptyRoutesNeedsNoAdmission proves a router that
// registers no application routes at all (only the operational
// endpoints) is not forced to supply an Admission it would never use.
func TestNewRouter_EmptyRoutesNeedsNoAdmission(t *testing.T) {
	if _, err := api.NewRouter(api.RouterConfig{AllowUnauthenticated: true}); err != nil {
		t.Errorf("NewRouter with no Routes and no Admission failed: %v", err)
	}
}
