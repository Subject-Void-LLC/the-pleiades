// Tests for the pin confirmation route, against a real file-backed
// inventory: only the pin recorded on first use, and trusted, is confirmed,
// only by a caller holding inventory:assert, and nothing is written by a
// refusal.
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// firstUsePinAPI is the pin each test's devices recorded on first use.
var firstUsePinAPI = "sha256:" + strings.Repeat("ab", 32)

// pinRouterWith mounts the confirmation route over a file inventory holding
// api1 (a first-use pin it trusts), api2 (one its own tls_fingerprint
// overrides) and api3 (none), and returns the router and the inventory's
// path.
func pinRouterWith(t *testing.T, authenticate func(http.Handler) http.Handler, admission api.Admitter) (http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), inventory.DefaultInventoryFilename)
	hosts := "hosts:\n" +
		"  - {id: h1, name: api1, type: generic_http, properties: {base_url: \"https://10.0.0.1\"}}\n" +
		"  - {id: h2, name: api2, type: generic_http, properties: {base_url: \"https://10.0.0.2\", tls_fingerprint: \"sha256:" + strings.Repeat("ef", 32) + "\"}}\n" +
		"  - {id: h3, name: api3, type: generic_http, properties: {base_url: \"https://10.0.0.3\"}}\n"
	if err := os.WriteFile(path, []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := inventory.NewFileRepository(path, inventory.NewItemFactory())
	for _, name := range []string{"api1", "api2"} {
		item, err := repo.GetByName(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		item.(interface{ RecordFirstUsePin(string) }).RecordFirstUsePin(firstUsePinAPI)
		if err := repo.Save(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	return confirmRouter(t, repo, authenticate, admission), path
}

// confirmRouter mounts the confirmation route over repo.
func confirmRouter(t *testing.T, repo inventory.Repository, authenticate func(http.Handler) http.Handler, admission api.Admitter) http.Handler {
	t.Helper()
	handler := api.NewDeviceHandler(repo, inventory.NewItemFactory(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      authenticate,
		Admission: admission,
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{{
			Method: apispec.ConfirmTLSPin.Method, Pattern: apispec.ConfirmTLSPin.Pattern,
			Scope: apispec.ConfirmTLSPin.Scope, Rel: apispec.ConfirmTLSPin.Rel, Handler: handler.ConfirmTLSPin,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

// storedTLS returns name's TLS settings as the inventory at path holds them.
func storedTLS(t *testing.T, path, name string) devicetls.Settings {
	t.Helper()
	item, err := inventory.NewFileRepository(path, inventory.NewItemFactory()).GetByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return devicetls.For(item)
}

// TestConfirmTLSPin_ConfirmsTheRecordedPinAndRefusesTheRest walks each
// refusal, writing nothing, then the confirmation.
func TestConfirmTLSPin_ConfirmsTheRecordedPinAndRefusesTheRest(t *testing.T) {
	router, path := pinRouterWith(t, alwaysAuthenticated, &fakeAdmitter{})
	for _, tc := range []struct {
		name, device, body string
		status             int
		says               string
	}{
		{"a body that is no object", "api1", `[]`, http.StatusBadRequest, ""},
		{"not a pin", "api1", `{"fingerprint":"trust me"}`, http.StatusUnprocessableEntity, "must be the pin"},
		{"another pin", "api1", `{"fingerprint":"sha256:` + strings.Repeat("cd", 32) + `"}`, http.StatusUnprocessableEntity, firstUsePinAPI},
		{"overridden by its own", "api2", `{"fingerprint":"` + firstUsePinAPI + `"}`, http.StatusConflict, "overrides"},
		{"no first-use pin", "api3", `{"fingerprint":"` + firstUsePinAPI + `"}`, http.StatusNotFound, "no pin recorded"},
		{"no such device", "api9", `{"fingerprint":"` + firstUsePinAPI + `"}`, http.StatusNotFound, ""},
	} {
		rec := serveGrant(router, http.MethodPost, "/inventory/devices/"+tc.device+"/tls-pin/confirm", tc.body)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s: %d %s, want %d saying %q", tc.name, rec.Code, rec.Body.String(), tc.status, tc.says)
		}
	}
	if s := storedTLS(t, path, "api1"); s.FirstUsePin() != firstUsePinAPI {
		t.Fatalf("a refusal changed api1: first use %q, pins %q", s.FirstUsePin(), s.Pins())
	}

	rec := serveGrant(router, http.MethodPost, "/inventory/devices/api1/tls-pin/confirm", `{"fingerprint":"`+strings.ToUpper(firstUsePinAPI)+`"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pins":["`+firstUsePinAPI+`"]`) {
		t.Fatalf("confirm = %d %s", rec.Code, rec.Body.String())
	}
	if s := storedTLS(t, path, "api1"); s.FirstUsePin() != "" || len(s.Pins()) != 1 || s.Pins()[0] != firstUsePinAPI {
		t.Errorf("after confirming: first use %q, pins %q", s.FirstUsePin(), s.Pins())
	}
	item, _ := inventory.NewFileRepository(path, inventory.NewItemFactory()).GetByName(context.Background(), "api1")
	if _, present := item.Properties().Raw()[pkginventory.TLSFirstUseProperty]; present {
		t.Error("the first-use pin outlived its confirmation")
	}
}

// firstUseStub is a stored device trusting a first-use pin that cannot
// record a confirmation: no record.Base behind it.
type firstUseStub struct {
	*inventorytest.Stub
	tls devicetls.Settings
}

// TLSSettings implements devicetls.Configured.
func (s firstUseStub) TLSSettings() devicetls.Settings { return s.tls }

// TestConfirmTLSPin_WhatTheStoreRefusesIsTheStores: a device the store
// cannot change answers internal error with nothing of the store's own
// words, and a read-only inventory's refusal is a conflict, writing nothing.
func TestConfirmTLSPin_WhatTheStoreRefusesIsTheStores(t *testing.T) {
	settings, err := devicetls.Parse(pkginventory.NewProperties(map[string]pkginventory.PropertyValue{devicetls.FirstUseProperty: firstUsePinAPI}))
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubDeviceRepo{item: firstUseStub{Stub: &inventorytest.Stub{StubName: "api1"}, tls: settings}}
	body := `{"fingerprint":"` + firstUsePinAPI + `"}`
	rec := serveGrant(confirmRouter(t, stub, alwaysAuthenticated, &fakeAdmitter{}), http.MethodPost, "/inventory/devices/api1/tls-pin/confirm", body)
	if rec.Code != http.StatusInternalServerError || stub.saved != nil || strings.Contains(rec.Body.String(), "cannot confirm") {
		t.Errorf("a device that cannot confirm: %d %s, saved %v", rec.Code, rec.Body.String(), stub.saved)
	}

	_, path := pinRouterWith(t, alwaysAuthenticated, &fakeAdmitter{})
	readOnly := inventory.NewReadOnlyRepository(inventory.NewFileRepository(path, inventory.NewItemFactory()))
	if rec := serveGrant(confirmRouter(t, readOnly, alwaysAuthenticated, &fakeAdmitter{}), http.MethodPost, "/inventory/devices/api1/tls-pin/confirm", body); rec.Code != http.StatusConflict {
		t.Errorf("a read-only inventory: %d %s, want 409", rec.Code, rec.Body.String())
	}
	if s := storedTLS(t, path, "api1"); s.FirstUsePin() != firstUsePinAPI {
		t.Error("a read-only inventory was written")
	}
}

// TestConfirmTLSPin_TheOnboarderDoesNotVouch: through the real token
// authentication and scope rule, an operator holding inventory:write and
// inventory:onboard, who may record a pin on first use, may not confirm
// it; an admin may.
func TestConfirmTLSPin_TheOnboarderDoesNotVouch(t *testing.T) {
	if apispec.ConfirmTLSPin.Scope != auth.ScopeInventoryAssert {
		t.Fatalf("confirming needs %s", apispec.ConfirmTLSPin.Scope)
	}
	issuer := authtest.New(t, "pin-test-issuer", "pin-test-audience")
	router, path := pinRouterWith(t, api.AuthMiddleware(issuer.Evaluator()), auth.Admission{Chain: auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}})
	send := func(id *auth.Identity) int {
		req := httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/inventory/devices/api1/tls-pin/confirm", strings.NewReader(`{"fingerprint":"`+firstUsePinAPI+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", issuer.BearerToken(t, id))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	operator := &auth.Identity{Subject: "operator", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryWrite, auth.ScopeInventoryOnboard}}
	if code := send(operator); code != http.StatusForbidden {
		t.Errorf("confirm by the onboarding operator = %d, want 403", code)
	}
	if s := storedTLS(t, path, "api1"); s.FirstUsePin() != firstUsePinAPI {
		t.Fatal("a refused confirmation was written")
	}
	admin := &auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scopes: auth.ScopesForRole(auth.RoleAdmin)}
	if code := send(admin); code != http.StatusOK {
		t.Errorf("confirm by an admin = %d, want 200", code)
	}
}
