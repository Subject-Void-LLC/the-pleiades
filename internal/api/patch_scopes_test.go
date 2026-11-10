// This file holds the owner's decision of 2026-11-10 that a PATCH or PUT
// route requires the resource's read scope as well as its write scope,
// through the real pipeline: a real token issuer, the real admission chain
// and HATEOAS generator, the real router and the real inventory store.
//
// A PATCH or PUT answers with the stored record, and validates the body
// against the record as it would be saved. A token holding only the write
// scope could therefore read any record by id with PATCH {} (the response),
// learn its organization by echoing guesses (200 against the fixed-field
// 400), and probe stored state through a merged-record refusal. Requiring
// read, checked before the body is read, closes all three at once.
package api_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// patchScopeSeq names each fixture's database.
var patchScopeSeq atomic.Int64

// patchScopeFixture is the real pipeline over one stored inventory.
type patchScopeFixture struct {
	router http.Handler
	issuer *authtest.Issuer
	id     int
}

// newPatchScopeFixture wires the inventory routes the Controller mounts,
// behind the Controller's own admission chain.
func newPatchScopeFixture(t *testing.T) *patchScopeFixture {
	t.Helper()
	client := newSerializedSQLiteClient(t, fmt.Sprintf("patch-scopes-%d", patchScopeSeq.Add(1)))
	org := client.Organization.Create().SetName("acme").SaveX(t.Context())
	store := inventory.NewEntSetStore(client)
	set, err := store.Create(t.Context(), inventory.Set{Name: "edge", OrganizationID: org.ID, Owner: "alice"})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	issuer := authtest.New(t, "patch-issuer", "patch-audience")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}
	handler := api.NewInventoryHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      api.AuthMiddleware(issuer.Evaluator()),
		Admission: auth.Admission{Chain: chain},
		HATEOAS:   generator,
		Routes: []api.Route{
			apispec.GetInventory.Route(handler.Get),
			apispec.UpdateInventory.Route(handler.Update),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return &patchScopeFixture{router: router, issuer: issuer, id: set.ID}
}

// do sends one request as id with body, and returns the status and body.
func (f *patchScopeFixture) do(t *testing.T, method, path, body string, id *auth.Identity) (int, string, http.Header) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", f.issuer.BearerToken(t, id))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String(), rec.Header()
}

// The identities the cases use. Operator, not admin: an admin passes every
// scope check whatever its token carries.
var (
	writeOnlyIdentity = &auth.Identity{Subject: "writer@example.com", Role: auth.RoleOperator,
		Scopes: []auth.Scope{auth.ScopeInventoryWrite}}
	readAndWriteIdentity = &auth.Identity{Subject: "operator@example.com", Role: auth.RoleOperator,
		Scopes: []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite}}
)

// TestPatch_AWriteScopeAloneIsRefusedBeforeTheBodyIsRead holds the one
// refusal: an existing id, a missing id and a body that is not JSON all
// answer the same 403, so a write-only token learns nothing from a PATCH.
// It used to answer 200 with the whole record, 404 and 400.
func TestPatch_AWriteScopeAloneIsRefusedBeforeTheBodyIsRead(t *testing.T) {
	t.Parallel()
	f := newPatchScopeFixture(t)
	existing := fmt.Sprintf("/api/v1/inventories/%d", f.id)

	var answers []string
	for _, c := range []struct{ path, body string }{
		{existing, `{}`},
		{existing, fmt.Sprintf(`{"organization":%d}`, f.id+1)},
		{"/api/v1/inventories/4242", `{}`},
		{existing, `not json`},
	} {
		status, body, _ := f.do(t, http.MethodPatch, c.path, c.body, writeOnlyIdentity)
		if status != http.StatusForbidden {
			t.Errorf("PATCH %s %s as write-only = %d %s, want 403", c.path, c.body, status, body)
		}
		answers = append(answers, fmt.Sprintf("%d %s", status, body))
	}
	for _, a := range answers[1:] {
		if a != answers[0] {
			t.Errorf("write-only answers differ: %q and %q, want one refusal", answers[0], a)
		}
	}

	if status, body, _ := f.do(t, http.MethodPatch, existing, `{}`, readAndWriteIdentity); status != http.StatusOK {
		t.Errorf("PATCH {} with read and write = %d %s, want 200", status, body)
	}
}

// TestPatch_IsNotAdvertisedToAWriteScopeAlone holds the advertising half to
// the same rule: OPTIONS on the inventory names PATCH in Allow only for a
// caller holding both scopes, so a client is never told it may call a route
// that will refuse it.
func TestPatch_IsNotAdvertisedToAWriteScopeAlone(t *testing.T) {
	t.Parallel()
	f := newPatchScopeFixture(t)
	path := fmt.Sprintf("/api/v1/inventories/%d", f.id)

	_, _, header := f.do(t, http.MethodOptions, path, "", writeOnlyIdentity)
	if allow := header.Get("Allow"); strings.Contains(allow, http.MethodPatch) {
		t.Errorf("Allow for a write-only caller = %q, want no PATCH", allow)
	}
	_, _, header = f.do(t, http.MethodOptions, path, "", readAndWriteIdentity)
	if allow := header.Get("Allow"); !strings.Contains(allow, http.MethodPatch) {
		t.Errorf("Allow for a read and write caller = %q, want PATCH", allow)
	}
}

// TestAlsoScopes_EveryDeclaringRouteRefusesAMissingScope mounts every
// apispec endpoint that declares AlsoScopes through the real router and
// admission chain, with a handler that records being reached, and holds each
// to the rule: a token missing any one of the route's scopes gets 403 and
// never reaches the handler, and one holding all of them does. It covers
// every route the table names, where the inventory tests above cover one end
// to end.
func TestAlsoScopes_EveryDeclaringRouteRefusesAMissingScope(t *testing.T) {
	t.Parallel()
	issuer := authtest.New(t, "also-issuer", "also-audience")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}

	var reached atomic.Int64
	var routes []api.Route
	var declaring []apispec.Endpoint
	for _, ep := range apispec.Endpoints {
		routes = append(routes, ep.Route(func(w http.ResponseWriter, _ *http.Request) {
			reached.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}))
		if len(ep.AlsoScopes) > 0 {
			declaring = append(declaring, ep)
		}
	}
	if len(declaring) < 34 {
		t.Fatalf("%d endpoints declare AlsoScopes, want every PATCH, PUT, record-answering POST and action route", len(declaring))
	}
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      api.AuthMiddleware(issuer.Evaluator()),
		Admission: auth.Admission{Chain: chain},
		HATEOAS:   generator,
		Routes:    routes,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	for _, ep := range declaring {
		path := api.APIVersionPrefix + strings.NewReplacer("{id}", "1", "{name}", "edge-1").Replace(ep.Pattern)
		send := func(scopes ...auth.Scope) int {
			req := httptest.NewRequest(ep.Method, path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", issuer.BearerToken(t, &auth.Identity{
				Subject: "probe@example.com", Role: auth.RoleOperator, Scopes: scopes,
			}))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			return rec.Code
		}
		all := append([]auth.Scope{ep.Scope}, ep.AlsoScopes...)
		// Each scope left out in turn: every one is required.
		for skip := range all {
			held := append(append([]auth.Scope(nil), all[:skip]...), all[skip+1:]...)
			before := reached.Load()
			if code := send(held...); code != http.StatusForbidden || reached.Load() != before {
				t.Errorf("%s %s without %s = %d (handler reached: %v), want 403 and no handler", ep.Method, ep.Pattern, all[skip], code, reached.Load() != before)
			}
		}
		if code := send(all...); code != http.StatusNoContent {
			t.Errorf("%s %s with %v = %d, want the handler's 204", ep.Method, ep.Pattern, all, code)
		}
	}

	// The advertising half for a route with two further scopes: binding a
	// template's credentials needs credential:write, credential:read and
	// template:read, so Allow names PUT only for a caller holding all three.
	allow := func(scopes ...auth.Scope) string {
		req := httptest.NewRequest(http.MethodOptions, api.APIVersionPrefix+"/templates/1/credentials", nil)
		req.Header.Set("Authorization", issuer.BearerToken(t, &auth.Identity{
			Subject: "probe@example.com", Role: auth.RoleOperator, Scopes: scopes,
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Header().Get("Allow")
	}
	if got := allow(auth.ScopeCredentialWrite, auth.ScopeCredentialRead); strings.Contains(got, http.MethodPut) {
		t.Errorf("Allow without template:read = %q, want no PUT", got)
	}
	if got := allow(auth.ScopeCredentialWrite, auth.ScopeCredentialRead, auth.ScopeTemplateRead); !strings.Contains(got, http.MethodPut) {
		t.Errorf("Allow with all three scopes = %q, want PUT", got)
	}
}
