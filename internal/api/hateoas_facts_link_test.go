// Phase 41: a device's response links to its facts, through the real route
// table's declaration (apispec.ListDeviceFacts.LinkedFrom), offered only to a
// caller who may read them, and never added to the device's Allow header.
package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// factsLinkRouter is newGateFixture's real pipeline with the device's facts
// route mounted from the route table itself.
func factsLinkRouter(t *testing.T) (http.Handler, *authtest.Issuer) {
	t.Helper()
	f := newGateFixture(t)
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(f.issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	devices := api.NewDeviceHandler(f.repo, inventory.NewItemFactory(), logger)
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    logger,
		Auth:      api.AuthMiddleware(f.issuer.Evaluator()),
		Admission: auth.Admission{Chain: chain, Recorder: &countingRecorder{}},
		HATEOAS:   generator,
		Routes: []api.Route{
			apispec.GetDevice.Route(devices.Get),
			apispec.DeleteDevice.Route(devices.Delete),
			apispec.ListDeviceFacts.Route(devices.Facts),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router, f.issuer
}

func TestDevice_LinksToItsFactsForACallerWhoMayReadThem(t *testing.T) {
	router, issuer := factsLinkRouter(t)
	get := func(scopes ...auth.Scope) deviceBody {
		req := httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/"+gateDeviceName, nil)
		req.Header.Set("Authorization", issuer.BearerToken(t, &auth.Identity{Subject: "u", Scopes: scopes}))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET device = %d: %s", rr.Code, rr.Body)
		}
		var body deviceBody
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	body := get(auth.ScopeInventoryRead)
	var found bool
	for _, l := range *body.Links {
		if l.Rel == string(auth.RelFacts) {
			found = true
			if want := api.APIVersionPrefix + "/inventory/devices/" + gateDeviceName + "/facts"; l.Href != want || l.Method != http.MethodGet {
				t.Errorf("facts link = %+v, want GET %s", l, want)
			}
		}
	}
	if !found {
		t.Fatalf("links %v carry no facts link", body.rels())
	}

	// The device's own Allow header names its own methods only.
	req := httptest.NewRequest(http.MethodOptions, api.APIVersionPrefix+"/inventory/devices/"+gateDeviceName, nil)
	req.Header.Set("Authorization", issuer.BearerToken(t, &auth.Identity{Subject: "u", Scopes: []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite}}))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if allow := strings.Split(rr.Header().Get("Allow"), ", "); !slices.Equal(allow, []string{"DELETE", "GET", "OPTIONS"}) {
		t.Errorf("Allow = %v, want the device's own methods", allow)
	}
}

// TestDevice_TheFactsLinkFollowsTheFactsRoutesScope: the link is offered by
// the facts route's own scope, so a route table giving facts a scope the
// caller lacks withholds it even where the device itself is readable.
func TestDevice_TheFactsLinkFollowsTheFactsRoutesScope(t *testing.T) {
	f := newGateFixture(t)
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(f.issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	devices := api.NewDeviceHandler(f.repo, inventory.NewItemFactory(), logger)
	facts := apispec.ListDeviceFacts.Route(devices.Facts)
	facts.Scope = auth.ScopeInventoryWrite
	router, err := api.NewRouter(api.RouterConfig{
		Logger: logger, Auth: api.AuthMiddleware(f.issuer.Evaluator()),
		Admission: auth.Admission{Chain: chain, Recorder: &countingRecorder{}}, HATEOAS: generator,
		Routes: []api.Route{apispec.GetDevice.Route(devices.Get), facts},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/"+gateDeviceName, nil)
	req.Header.Set("Authorization", f.issuer.BearerToken(t, &auth.Identity{Subject: "u", Scopes: []auth.Scope{auth.ScopeInventoryRead}}))
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	var body deviceBody
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(body.rels(), string(auth.RelFacts)) {
		t.Fatalf("a caller without the facts route's scope was offered its link: %v", body.rels())
	}
}

func TestNewRouter_RefusesALinkThatCouldNotBeBuilt(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) {}
	parent := api.Route{Method: http.MethodGet, Pattern: "/things/{id}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: ok}
	child := func(pattern, from string, as auth.LinkRel) api.Route {
		return api.Route{Method: http.MethodGet, Pattern: pattern, Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, LinkedFrom: from, LinkedAs: as, Handler: ok}
	}
	for name, routes := range map[string][]api.Route{
		"no relation":               {parent, child("/things/{id}/facts", "/things/{id}", "")},
		"a relation, no parent":     {parent, child("/things/{id}/facts", "", auth.RelFacts)},
		"an unknown parent":         {parent, child("/things/{id}/facts", "/others/{id}", auth.RelFacts)},
		"not under the parent":      {parent, child("/elsewhere/{id}/facts", "/things/{id}", auth.RelFacts)},
		"the parent itself":         {parent, child("/things/{id}/", "/things/{id}", auth.RelFacts)},
		"a parameter of its own":    {parent, child("/things/{id}/facts/{key}", "/things/{id}", auth.RelFacts)},
		"a relation the parent has": {parent, child("/things/{id}/facts", "/things/{id}", auth.RelSelf)},
		"two links under one name": {parent, child("/things/{id}/facts", "/things/{id}", auth.RelFacts),
			child("/things/{id}/history", "/things/{id}", auth.RelFacts)},
	} {
		if _, err := api.NewRouter(api.RouterConfig{Routes: routes}); err == nil {
			t.Errorf("%s: NewRouter accepted the route table", name)
		}
	}
}
