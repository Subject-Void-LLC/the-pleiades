package api_test

import (
	"net/http"
	"strings"
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
		HATEOAS:              allowAllGenerator(t),
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
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: noopHandler},
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
		HATEOAS:              allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: noopHandler},
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeRunbookExecute, Rel: auth.RelSelf, Handler: noopHandler},
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

// TestNewRouter_RejectsRoutesWithNilHATEOAS proves a route table cannot
// exist with nothing able to resolve the relations it declares.
//
// This is the same shape as the nil-Admission check above, one field
// over, and it is what closes Phase 13's own vacuous-pass hole. The
// middleware this phase deleted degraded silently to "a self link and
// nothing else" when its generator was nil, which is exactly how a
// Release Gate reading "the _links array omits the delete URL" passes
// while omitting every URL there is.
func TestNewRouter_RejectsRoutesWithNilHATEOAS(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Admission:            &fakeAdmitter{},
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: noopHandler},
		},
	})
	if err == nil {
		t.Fatal("NewRouter with Routes set and HATEOAS nil succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "HATEOAS") {
		t.Errorf("error does not name the missing field: %v", err)
	}
}

// TestNewRouter_RejectsRouteWithEmptyRel proves a route cannot be
// registered without naming the relation it appears under. An unnamed
// affordance is one no response can describe and no Allow header can
// list, which makes it undiscoverable in exactly the way HATEOAS exists
// to prevent.
func TestNewRouter_RejectsRouteWithEmptyRel(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Admission:            &fakeAdmitter{},
		HATEOAS:              allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Handler: noopHandler},
		},
	})
	if err == nil {
		t.Fatal("NewRouter with an empty Route.Rel succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "Rel") {
		t.Errorf("error does not name the missing field: %v", err)
	}
}

// TestNewRouter_RejectsDuplicateRelOnOnePattern proves one resource
// cannot declare the same relation twice. Two entries sharing a rel in
// one _links array are two affordances a client cannot tell apart, and
// the permitted-set correlation in linksFor would have no way to decide
// which method each belongs to.
func TestNewRouter_RejectsDuplicateRelOnOnePattern(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Admission:            &fakeAdmitter{},
		HATEOAS:              allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: noopHandler},
			{Method: http.MethodDelete, Pattern: "/jobs", Scope: auth.ScopeInventoryWrite, Rel: auth.RelSelf, Handler: noopHandler},
		},
	})
	if err == nil {
		t.Fatal("NewRouter with two Routes sharing a Rel on one Pattern succeeded, want an error")
	}
}

// TestNewRouter_RejectsRouteClaimingOptions proves a handler cannot take
// the OPTIONS method for itself. The router registers its own OPTIONS
// handler on every pattern (registerOptions), and a handler-supplied one
// would shadow it, silently turning the scope-filtered Allow header
// PLAN.md Section 21.2 requires back into whatever that handler wrote.
func TestNewRouter_RejectsRouteClaimingOptions(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Admission:            &fakeAdmitter{},
		HATEOAS:              allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodOptions, Pattern: "/jobs", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: noopHandler},
		},
	})
	if err == nil {
		t.Fatal("NewRouter with a Route claiming OPTIONS succeeded, want an error")
	}
}

// TestNewRouter_RejectsWildcardPattern proves a wildcard route cannot be
// registered. A wildcard has no stable href to build a link from, so any
// _links entry naming it would be a guess about a URL the server may not
// actually serve.
func TestNewRouter_RejectsWildcardPattern(t *testing.T) {
	_, err := api.NewRouter(api.RouterConfig{
		AllowUnauthenticated: true,
		Admission:            &fakeAdmitter{},
		HATEOAS:              allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/files/*", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: noopHandler},
		},
	})
	if err == nil {
		t.Fatal("NewRouter with a wildcard Pattern succeeded, want an error")
	}
}
