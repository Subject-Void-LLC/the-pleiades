// Tests for the grant routes, against a real file-backed inventory.
package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// grantRouter mounts both grant routes over a file inventory holding the
// linux_server w1, and returns the router and the inventory's path.
func grantRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	return grantRouterWith(t, alwaysAuthenticated, &fakeAdmitter{})
}

// grantRouterWith is grantRouter with the authentication and admission
// given.
func grantRouterWith(t *testing.T, authenticate func(http.Handler) http.Handler, admission api.Admitter) (http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), inventory.DefaultInventoryFilename)
	if err := os.WriteFile(path, []byte("hosts:\n  - id: h1\n    name: w1\n    type: linux_server\n    properties: {host: 10.0.0.1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := api.NewDeviceHandler(inventory.NewFileRepository(path, inventory.NewItemFactory()), inventory.NewItemFactory(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	var routes []api.Route
	for _, ep := range []struct {
		e apispec.Endpoint
		h http.HandlerFunc
	}{{apispec.GrantCapability, handler.GrantCapability}, {apispec.RevokeCapability, handler.RevokeCapability}} {
		routes = append(routes, api.Route{Method: ep.e.Method, Pattern: ep.e.Pattern, Scope: ep.e.Scope, Rel: ep.e.Rel, Handler: ep.h})
	}
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      authenticate,
		Admission: admission,
		HATEOAS:   allowAllGenerator(t),
		Routes:    routes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return router, path
}

// serveGrant sends one request to router and returns the response.
func serveGrant(router http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, api.APIVersionPrefix+target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestGrantRoutes_AnAssertionIsRecordedAndRevoked: a grant is saved as an
// assertion naming the caller and the reason, and revoking it removes it;
// a second revoke finds nothing.
func TestGrantRoutes_AnAssertionIsRecordedAndRevoked(t *testing.T) {
	router, path := grantRouter(t)
	rec := serveGrant(router, http.MethodPost, "/inventory/devices/w1/capabilities",
		`{"capability":"acme.widget_api","properties":{"endpoint":"https://w","port":443},"reason":"the lab controller"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"assertion"`) {
		t.Fatalf("grant = %d %s", rec.Code, rec.Body.String())
	}
	item, err := inventory.NewFileRepository(path, inventory.NewItemFactory()).GetByName(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	grants, _, err := pkginventory.GrantsFrom(item.Properties())
	if err != nil || len(grants) != 1 || grants[0].Reason != "the lab controller" || grants[0].By == "" {
		t.Fatalf("stored grants = %+v, %v; want the assertion with its reason and its author", grants, err)
	}

	if rec := serveGrant(router, http.MethodDelete, "/inventory/devices/w1/capabilities/acme.widget_api", ""); rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body.String())
	}
	if rec := serveGrant(router, http.MethodDelete, "/inventory/devices/w1/capabilities/acme.widget_api", ""); rec.Code != http.StatusNotFound {
		t.Errorf("a second revoke = %d, want 404", rec.Code)
	}
}

// TestGrantRoutes_RefuseWhatTheyCannotRecord: a compiled capability, a
// name that is no data capability's, no reason, a malformed property and
// an unknown device are refused, and nothing is written.
func TestGrantRoutes_RefuseWhatTheyCannotRecord(t *testing.T) {
	router, path := grantRouter(t)
	for _, tc := range []struct {
		name, target, body string
		status             int
	}{
		{"a compiled capability", "w1", `{"capability":"SSHTransportCapable","reason":"x"}`, http.StatusUnprocessableEntity},
		{"not a data name", "w1", `{"capability":"widget","reason":"x"}`, http.StatusUnprocessableEntity},
		{"no reason", "w1", `{"capability":"acme.widget_api"}`, http.StatusUnprocessableEntity},
		{"a malformed property", "w1", `{"capability":"acme.widget_api","reason":"x","properties":{"Bad-Name":"y"}}`, http.StatusUnprocessableEntity},
		{"a nested property", "w1", `{"capability":"acme.widget_api","reason":"x","properties":{"list":["a"]}}`, http.StatusUnprocessableEntity},
		{"a secret-named property", "w1", `{"capability":"acme.widget_api","reason":"x","properties":{"api_token":"t0ken"}}`, http.StatusUnprocessableEntity},
		{"an unknown device", "nobody", `{"capability":"acme.widget_api","reason":"x"}`, http.StatusNotFound},
	} {
		if rec := serveGrant(router, http.MethodPost, "/inventory/devices/"+tc.target+"/capabilities", tc.body); rec.Code != tc.status {
			t.Errorf("%s: %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.status)
		}
	}
	item, err := inventory.NewFileRepository(path, inventory.NewItemFactory()).GetByName(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := item.Properties().Raw()[pkginventory.GrantedProperty]; present {
		t.Error("a refused grant was written")
	}
}

// TestGrantRoutes_ScopeIsAdminsAlone: inventory:assert is the routes'
// scope, and neither inventory:write nor any role but admin carries it.
func TestGrantRoutes_ScopeIsAdminsAlone(t *testing.T) {
	if apispec.GrantCapability.Scope != auth.ScopeInventoryAssert || apispec.RevokeCapability.Scope != auth.ScopeInventoryAssert {
		t.Fatal("the grant routes do not need inventory:assert")
	}
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator} {
		id := &auth.Identity{Subject: "x", Role: role, Scopes: auth.ScopesForRole(role)}
		if id.HasScope(auth.ScopeInventoryAssert) {
			t.Errorf("role %s carries inventory:assert", role)
		}
	}
	writer := &auth.Identity{Subject: "w", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryWrite, auth.ScopeInventoryOnboard}}
	if writer.HasScope(auth.ScopeInventoryAssert) {
		t.Error("inventory:write or inventory:onboard carries inventory:assert")
	}
	admin := auth.ScopesForRole(auth.RoleAdmin)
	found := false
	for _, s := range admin {
		found = found || s == auth.ScopeInventoryAssert
	}
	if !found {
		t.Error("admin's scope list does not record inventory:assert")
	}
}

// TestGrantRoutes_AnInventoryWriterIsRefused: through the mounted routes,
// the real token authentication and the real scope rule, a caller holding
// inventory:write and inventory:onboard but not inventory:assert is
// refused both, and nothing is written; an admin is allowed.
func TestGrantRoutes_AnInventoryWriterIsRefused(t *testing.T) {
	issuer := authtest.New(t, "grant-test-issuer", "grant-test-audience")
	router, path := grantRouterWith(t, api.AuthMiddleware(issuer.Evaluator()), auth.Admission{Chain: auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}})
	send := func(id *auth.Identity, method, target, body string) int {
		req := httptest.NewRequest(method, api.APIVersionPrefix+target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", issuer.BearerToken(t, id))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	writer := &auth.Identity{Subject: "writer", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryWrite, auth.ScopeInventoryOnboard}}
	if code := send(writer, http.MethodPost, "/inventory/devices/w1/capabilities", `{"capability":"acme.widget_api","reason":"x"}`); code != http.StatusForbidden {
		t.Errorf("grant by an inventory writer = %d, want 403", code)
	}
	if code := send(writer, http.MethodDelete, "/inventory/devices/w1/capabilities/acme.widget_api", ""); code != http.StatusForbidden {
		t.Errorf("revoke by an inventory writer = %d, want 403", code)
	}
	item, err := inventory.NewFileRepository(path, inventory.NewItemFactory()).GetByName(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := item.Properties().Raw()[pkginventory.GrantedProperty]; present {
		t.Error("a refused grant was written")
	}
	admin := &auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scopes: auth.ScopesForRole(auth.RoleAdmin)}
	if code := send(admin, http.MethodPost, "/inventory/devices/w1/capabilities", `{"capability":"acme.widget_api","reason":"x"}`); code != http.StatusOK {
		t.Errorf("grant by an admin = %d, want 200", code)
	}
}
