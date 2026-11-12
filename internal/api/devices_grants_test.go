// Tests for the grant routes, against a real file-backed inventory.
package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// widgetDigest is a definition digest of the shape a client supplies,
// computed from a definition as `pleiades doc` would print it.
var widgetDigest = capability.DefinitionDigest(capability.DataDefinition{Name: "acme.widget_api", Provider: "widgets"})

// grantBody is a grant request's body for acme.widget_api with reason and
// the definition digest, and props as its properties when not empty.
func grantBody(reason, props string) string {
	body := `{"capability":"acme.widget_api","reason":"` + reason + `","definition":"` + widgetDigest + `"`
	if props != "" {
		body += `,"properties":` + props
	}
	return body + "}"
}

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
		grantBody("the lab controller", `{"endpoint":"https://w","port":443}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"assertion"`) || !strings.Contains(rec.Body.String(), `"definition":"`+widgetDigest+`"`) {
		t.Fatalf("grant = %d %s", rec.Code, rec.Body.String())
	}
	item, err := inventory.NewFileRepository(path, inventory.NewItemFactory()).GetByName(context.Background(), "w1")
	if err != nil {
		t.Fatal(err)
	}
	grants, _, err := pkginventory.GrantsFrom(item.Properties())
	if err != nil || len(grants) != 1 || grants[0].Reason != "the lab controller" || grants[0].By == "" || grants[0].Definition != widgetDigest {
		t.Fatalf("stored grants = %+v, %v; want the assertion with its reason, its author and its definition digest", grants, err)
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
		{"a compiled capability", "w1", `{"capability":"SSHTransportCapable","reason":"x","definition":"` + widgetDigest + `"}`, http.StatusUnprocessableEntity},
		{"not a data name", "w1", `{"capability":"widget","reason":"x","definition":"` + widgetDigest + `"}`, http.StatusUnprocessableEntity},
		{"no reason", "w1", `{"capability":"acme.widget_api","definition":"` + widgetDigest + `"}`, http.StatusUnprocessableEntity},
		{"a malformed property", "w1", grantBody("x", `{"Bad-Name":"y"}`), http.StatusUnprocessableEntity},
		{"a nested property", "w1", grantBody("x", `{"list":["a"]}`), http.StatusUnprocessableEntity},
		{"a secret-named property", "w1", grantBody("x", `{"api_token":"t0ken"}`), http.StatusUnprocessableEntity},
		{"a property shaped like a secret", "w1", grantBody("x", `{"endpoint":"https://ops:hunter2-Orchid@widgets.example/api"}`), http.StatusUnprocessableEntity},
		{"an unknown device", "nobody", grantBody("x", ""), http.StatusNotFound},
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
	if code := send(writer, http.MethodPost, "/inventory/devices/w1/capabilities", grantBody("x", "")); code != http.StatusForbidden {
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
	if code := send(admin, http.MethodPost, "/inventory/devices/w1/capabilities", grantBody("x", "")); code != http.StatusOK {
		t.Errorf("grant by an admin = %d, want 200", code)
	}
}

// TestGrantRoutes_RefuseABadRequest: a name over the bound, a body that is
// not JSON, a grant carrying no definition digest or one of the wrong
// shape, and a revoke of a name that is no data capability's or of an
// unknown device each answer with their own status, and no refused grant
// is written.
func TestGrantRoutes_RefuseABadRequest(t *testing.T) {
	router, path := grantRouter(t)
	long := strings.Repeat("w", 300)
	digest := strings.TrimPrefix(widgetDigest, capability.DefinitionDigestPrefix)
	for _, tc := range []struct {
		name, method, target, body string
		status                     int
	}{
		{"a grant to a name over the bound", http.MethodPost, "/inventory/devices/" + long + "/capabilities", `{}`, http.StatusBadRequest},
		{"a body that is not JSON", http.MethodPost, "/inventory/devices/w1/capabilities", `{`, http.StatusBadRequest},
		{"a grant with no definition digest", http.MethodPost, "/inventory/devices/w1/capabilities", `{"capability":"acme.widget_api","reason":"x"}`, http.StatusUnprocessableEntity},
		{"a definition digest without its prefix", http.MethodPost, "/inventory/devices/w1/capabilities", `{"capability":"acme.widget_api","reason":"x","definition":"` + digest + `"}`, http.StatusUnprocessableEntity},
		{"a definition digest in upper case", http.MethodPost, "/inventory/devices/w1/capabilities", `{"capability":"acme.widget_api","reason":"x","definition":"` + strings.ToUpper(widgetDigest) + `"}`, http.StatusUnprocessableEntity},
		{"a definition digest cut short", http.MethodPost, "/inventory/devices/w1/capabilities", `{"capability":"acme.widget_api","reason":"x","definition":"` + widgetDigest[:40] + `"}`, http.StatusUnprocessableEntity},
		{"a definition digest that is not text", http.MethodPost, "/inventory/devices/w1/capabilities", `{"capability":"acme.widget_api","reason":"x","definition":7}`, http.StatusBadRequest},
		{"a revoke from a name over the bound", http.MethodDelete, "/inventory/devices/" + long + "/capabilities/acme.widget_api", "", http.StatusBadRequest},
		{"a revoke of no data capability", http.MethodDelete, "/inventory/devices/w1/capabilities/SSHTransportCapable", "", http.StatusBadRequest},
		{"a revoke from an unknown device", http.MethodDelete, "/inventory/devices/nobody/capabilities/acme.widget_api", "", http.StatusNotFound},
	} {
		if rec := serveGrant(router, tc.method, tc.target, tc.body); rec.Code != tc.status {
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

// TestGrantRoutes_AMalformedStoredGrantIsNotWrittenOver: a stored device
// whose granted property RecordGrant did not write is not built at all, so
// it is neither granted to nor revoked from; the routes answer a server
// error and leave it.
func TestGrantRoutes_AMalformedStoredGrantIsNotWrittenOver(t *testing.T) {
	router, path := grantRouter(t)
	if err := os.WriteFile(path, []byte("hosts:\n  - id: h1\n    name: w1\n    type: linux_server\n    properties: {host: 10.0.0.1, granted: acme.widget_api}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if rec := serveGrant(router, http.MethodPost, "/inventory/devices/w1/capabilities", grantBody("x", "")); rec.Code != http.StatusInternalServerError {
		t.Errorf("a grant over a malformed one = %d %s, want 500", rec.Code, rec.Body.String())
	}
	if rec := serveGrant(router, http.MethodDelete, "/inventory/devices/w1/capabilities/acme.widget_api", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("a revoke from a malformed one = %d %s, want 500", rec.Code, rec.Body.String())
	}
}

// oneItemRepo answers every read with item and every save with saveErr.
type oneItemRepo struct {
	api.DeviceRepository
	item    pkginventory.InventoryItem
	saveErr error
}

func (r oneItemRepo) GetByName(context.Context, string) (pkginventory.InventoryItem, error) {
	return r.item, nil
}

func (r oneItemRepo) Save(context.Context, pkginventory.InventoryItem) error { return r.saveErr }

// grantHolder is a device that takes grants, failing each write with err,
// and holding props.
type grantHolder struct {
	*inventorytest.Stub
	err error
}

func (g grantHolder) RecordGrant(pkginventory.Grant) error      { return g.err }
func (g grantHolder) RevokeGrant(capability.Name) (bool, error) { return g.err == nil, g.err }
func newGrantHolder(err error, props map[string]pkginventory.PropertyValue) grantHolder {
	return grantHolder{Stub: &inventorytest.Stub{StubName: "w1", Props: props}, err: err}
}

// TestGrantRoutes_ADeviceThatCannotTakeTheGrant: a device type with no
// grant to hold refuses a grant and has none to revoke; a write that fails
// is a server error naming nothing internal, as is a save that fails;
// and a grant recorded on a
// device whose grants cannot then be read answers with none, with no
// caller on the request named as its author.
func TestGrantRoutes_ADeviceThatCannotTakeTheGrant(t *testing.T) {
	var saveErr error
	serve := func(item pkginventory.InventoryItem, method, target, body string) *httptest.ResponseRecorder {
		handler := api.NewDeviceHandler(oneItemRepo{item: item, saveErr: saveErr}, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		router := chi.NewRouter()
		router.Post("/inventory/devices/{name}/capabilities", handler.GrantCapability)
		router.Delete("/inventory/devices/{name}/capabilities/{capability}", handler.RevokeCapability)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
		return rec
	}
	grant := grantBody("x", "")
	revoke := "/inventory/devices/w1/capabilities/acme.widget_api"

	plain := &inventorytest.Stub{StubName: "w1"}
	if rec := serve(plain, http.MethodPost, "/inventory/devices/w1/capabilities", grant); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a grant to a device that holds none = %d, want 422", rec.Code)
	}
	if rec := serve(plain, http.MethodDelete, revoke, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a revoke from a device that holds none = %d, want 404", rec.Code)
	}

	failing := newGrantHolder(errors.New("store: /var/lib/pleiades/inventory.yaml locked"), nil)
	for _, rec := range []*httptest.ResponseRecorder{
		serve(failing, http.MethodPost, "/inventory/devices/w1/capabilities", grant),
		serve(failing, http.MethodDelete, revoke, ""),
	} {
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "inventory.yaml") {
			t.Errorf("a failed write = %d %s, want 500 naming nothing internal", rec.Code, rec.Body.String())
		}
	}

	saveErr = errors.New("store: /var/lib/pleiades/inventory.yaml read only")
	unsaved := newGrantHolder(nil, nil)
	for _, rec := range []*httptest.ResponseRecorder{
		serve(unsaved, http.MethodPost, "/inventory/devices/w1/capabilities", grant),
		serve(unsaved, http.MethodDelete, revoke, ""),
	} {
		if rec.Code < 500 || strings.Contains(rec.Body.String(), "inventory.yaml") {
			t.Errorf("a failed save = %d %s, want a server error naming nothing internal", rec.Code, rec.Body.String())
		}
	}
	saveErr = nil

	unreadable := newGrantHolder(nil, map[string]pkginventory.PropertyValue{pkginventory.GrantedProperty: "acme.widget_api"})
	rec := serve(unreadable, http.MethodPost, "/inventory/devices/w1/capabilities", grant)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"grants":[]`) {
		t.Errorf("a grant on a device whose grants cannot be read = %d %s, want 200 with none", rec.Code, rec.Body.String())
	}
}
