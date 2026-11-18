// Tests for the onboarding route: how each outcome is answered, and that
// its scope is its own.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
)

// onboardRouter mounts the onboarding route with o behind it.
func onboardRouter(t *testing.T, o api.Onboarder) http.Handler {
	t.Helper()
	return onboardRouterWith(t, o, alwaysAuthenticated, &fakeAdmitter{})
}

// onboardRouterWith is onboardRouter with the authentication and admission
// given.
func onboardRouterWith(t *testing.T, o api.Onboarder, authenticate func(http.Handler) http.Handler, admission api.Admitter) http.Handler {
	t.Helper()
	handler := api.NewDeviceHandler(&stubDeviceRepo{}, inventory.NewItemFactory(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if o != nil {
		handler.WithOnboarder(o)
	}
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      authenticate,
		Admission: admission,
		HATEOAS:   allowAllGenerator(t),
		// Mounted as cmd/controller mounts it, so its scopes are the spec's.
		Routes: []api.Route{apispec.OnboardDevice.Route(handler.Onboard)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

// TestDeviceOnboard_AnswersEachOutcome maps each onboarding outcome to its
// status, a failed probe still carries the result with its reason, and a
// success carries the result's warnings.
func TestDeviceOnboard_AnswersEachOutcome(t *testing.T) {
	const warning = "device api1 allows TLS 1.0 and 1.1 (tls_allow_deprecated_versions), which RFC 8996 deprecates"
	done := onboard.Result{Device: "api1", Type: "generic_http", Protocol: "http", PreviousState: "discovered", State: "active", Capabilities: []string{"HTTPAPICapable"}, Changed: true,
		Warnings: []string{warning}}
	failed := onboard.Result{Device: "api1", Type: "generic_http", Protocol: "http", PreviousState: "discovered", State: "discovered", Error: "the probe proved nothing: refused"}
	for _, tc := range []struct {
		name   string
		o      api.Onboarder
		status int
		body   string
	}{
		{"onboarded", func(context.Context, string, bool) (onboard.Result, error) { return done, nil }, http.StatusOK, `"state":"active"`},
		{"probe failed", func(context.Context, string, bool) (onboard.Result, error) {
			return failed, fmt.Errorf("%w: refused", onboard.ErrProbe)
		}, http.StatusBadGateway, `"error":"the probe proved nothing: refused"`},
		{"vendor type", func(context.Context, string, bool) (onboard.Result, error) {
			return onboard.Result{}, fmt.Errorf("x: %w", onboard.ErrNotOnboarded)
		}, http.StatusUnprocessableEntity, "generic"},
		{"administrator state", func(context.Context, string, bool) (onboard.Result, error) {
			return onboard.Result{}, fmt.Errorf("x: %w", onboard.ErrAdministratorState)
		}, http.StatusConflict, "administrator"},
		{"no such device", func(context.Context, string, bool) (onboard.Result, error) {
			return onboard.Result{}, inventory.ErrItemNotFound
		}, http.StatusNotFound, "not found"},
		{"a broken store", func(context.Context, string, bool) (onboard.Result, error) {
			return onboard.Result{}, fmt.Errorf("pq: relation devices at db.internal:5432")
		}, http.StatusInternalServerError, "internal error"},
		{"not enabled", nil, http.StatusNotImplemented, "not enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			onboardRouter(t, tc.o).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/inventory/devices/api1/onboard", nil))
			if rr.Code != tc.status || !strings.Contains(rr.Body.String(), tc.body) {
				t.Fatalf("%d %s, want %d containing %q", rr.Code, rr.Body.String(), tc.status, tc.body)
			}
			if strings.Contains(rr.Body.String(), "db.internal") {
				t.Error("the store's own message reached the caller")
			}
			if tc.status == http.StatusOK {
				var got map[string]any
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || got["_links"] == nil {
					t.Errorf("body %s is not a result with links", rr.Body.String())
				}
				// The warning a weakened record carries reaches the caller.
				if w, _ := got["warnings"].([]any); len(w) != 1 || w[0] != warning {
					t.Errorf("warnings %v, want the one the result carried", got["warnings"])
				}
			}
		})
	}
}

// TestDeviceOnboard_FirstUseIsAskedForOnce: tls_first_use reaches the
// onboarder as given, absent is no, and anything but one true or false is
// refused before anything is probed; the pin recorded comes back for review.
func TestDeviceOnboard_FirstUseIsAskedForOnce(t *testing.T) {
	pin := &onboard.FirstUsePin{Fingerprint: "sha256:" + strings.Repeat("ab", 32), KeyFingerprint: "spki-sha256:" + strings.Repeat("cd", 32), NotAfter: "2030-01-01"}
	for _, tc := range []struct {
		query  string
		status int
		asked  bool
		probed bool
	}{
		{"", http.StatusOK, false, true},
		{"?tls_first_use=false", http.StatusOK, false, true},
		{"?tls_first_use=true", http.StatusOK, true, true},
		{"?tls_first_use=yes", http.StatusBadRequest, false, false},
		{"?tls_first_use=true&tls_first_use=true", http.StatusBadRequest, false, false},
	} {
		var asked, probed bool
		router := onboardRouter(t, func(_ context.Context, _ string, firstUse bool) (onboard.Result, error) {
			asked, probed = firstUse, true
			res := onboard.Result{Device: "api1", State: "active"}
			if firstUse {
				res.FirstUse = pin
			}
			return res, nil
		})
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/inventory/devices/api1/onboard"+tc.query, nil))
		if rr.Code != tc.status || asked != tc.asked || probed != tc.probed {
			t.Errorf("%q: %d (asked %v, probed %v), want %d (asked %v, probed %v)", tc.query, rr.Code, asked, probed, tc.status, tc.asked, tc.probed)
		}
		if tc.asked && (!strings.Contains(rr.Body.String(), `"tls_first_use":{`) || !strings.Contains(rr.Body.String(), pin.Fingerprint)) {
			t.Errorf("%q: body %s does not list the pin recorded", tc.query, rr.Body.String())
		}
	}
}

// TestDeviceOnboard_FirstUseNeedsAssert: through the real token
// authentication and scope rule, an operator holding inventory:onboard and
// inventory:read onboards a device, and is refused first use before anything
// is probed, since it would send the device's credential to a certificate
// nobody checked; a caller holding inventory:assert besides, and an admin,
// may ask for it.
func TestDeviceOnboard_FirstUseNeedsAssert(t *testing.T) {
	issuer := authtest.New(t, "onboard-test-issuer", "onboard-test-audience")
	var asked []bool
	router := onboardRouterWith(t, func(_ context.Context, _ string, firstUse bool) (onboard.Result, error) {
		asked = append(asked, firstUse)
		return onboard.Result{Device: "api1", State: "active"}, nil
	}, api.AuthMiddleware(issuer.Evaluator()), auth.Admission{Chain: auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}})
	send := func(id *auth.Identity, query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/inventory/devices/api1/onboard"+query, nil)
		req.Header.Set("Authorization", issuer.BearerToken(t, id))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	onboarder := &auth.Identity{Subject: "operator", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryOnboard}}
	if rec := send(onboarder, ""); rec.Code != http.StatusOK {
		t.Fatalf("ordinary onboarding by an onboarder = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := send(onboarder, "?tls_first_use=false"); rec.Code != http.StatusOK {
		t.Errorf("tls_first_use=false by an onboarder = %d, want 200", rec.Code)
	}
	if rec := send(onboarder, "?tls_first_use=true"); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "inventory:assert") {
		t.Errorf("first use by an onboarder = %d %s, want 403 naming inventory:assert", rec.Code, rec.Body.String())
	}
	if len(asked) != 2 || asked[0] || asked[1] {
		t.Fatalf("the onboarder reached onboarding as %v, want twice without first use and never with it", asked)
	}
	asserter := &auth.Identity{Subject: "asserter", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryRead, auth.ScopeInventoryOnboard, auth.ScopeInventoryAssert}}
	admin := &auth.Identity{Subject: "admin", Role: auth.RoleAdmin, Scopes: auth.ScopesForRole(auth.RoleAdmin)}
	for name, id := range map[string]*auth.Identity{"inventory:assert": asserter, "an admin": admin} {
		if rec := send(id, "?tls_first_use=true"); rec.Code != http.StatusOK || !asked[len(asked)-1] {
			t.Errorf("first use by %s = %d %s, want 200 and onboarding with first use", name, rec.Code, rec.Body.String())
		}
	}
	operator := &auth.Identity{Subject: "o", Role: auth.RoleOperator, Scopes: auth.ScopesForRole(auth.RoleOperator)}
	if operator.HasScope(auth.ScopeInventoryAssert) {
		t.Error("the operator role carries inventory:assert, so every operator could record a pin on first use")
	}
}

// TestDeviceOnboard_ScopeIsItsOwn: the route needs inventory:onboard,
// which inventory:write does not carry, and an operator holds it.
func TestDeviceOnboard_ScopeIsItsOwn(t *testing.T) {
	if apispec.OnboardDevice.Scope != auth.ScopeInventoryOnboard {
		t.Fatalf("onboarding needs %s", apispec.OnboardDevice.Scope)
	}
	writer := &auth.Identity{Subject: "w", Role: auth.RoleOperator, Scopes: []auth.Scope{auth.ScopeInventoryWrite}}
	if writer.HasScope(auth.ScopeInventoryOnboard) {
		t.Error("inventory:write carries inventory:onboard")
	}
	operator := &auth.Identity{Subject: "o", Role: auth.RoleOperator, Scopes: auth.ScopesForRole(auth.RoleOperator)}
	if !operator.HasScope(auth.ScopeInventoryOnboard) {
		t.Error("an operator cannot onboard")
	}
	viewer := &auth.Identity{Subject: "v", Role: auth.RoleViewer, Scopes: auth.ScopesForRole(auth.RoleViewer)}
	if viewer.HasScope(auth.ScopeInventoryOnboard) {
		t.Error("a viewer can onboard")
	}
}
