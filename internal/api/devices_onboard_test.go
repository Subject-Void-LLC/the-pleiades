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
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
)

// onboardRouter mounts the onboarding route with o behind it.
func onboardRouter(t *testing.T, o api.Onboarder) http.Handler {
	t.Helper()
	handler := api.NewDeviceHandler(&stubDeviceRepo{}, inventory.NewItemFactory(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if o != nil {
		handler.WithOnboarder(o)
	}
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{{
			Method: apispec.OnboardDevice.Method, Pattern: apispec.OnboardDevice.Pattern,
			Scope: apispec.OnboardDevice.Scope, Rel: apispec.OnboardDevice.Rel, Handler: handler.Onboard,
		}},
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
	failed := onboard.Result{Device: "api1", Type: "generic_http", Protocol: "http", PreviousState: "discovered", State: "onboarding", Error: "the probe proved nothing: refused"}
	for _, tc := range []struct {
		name   string
		o      api.Onboarder
		status int
		body   string
	}{
		{"onboarded", func(context.Context, string) (onboard.Result, error) { return done, nil }, http.StatusOK, `"state":"active"`},
		{"probe failed", func(context.Context, string) (onboard.Result, error) {
			return failed, fmt.Errorf("%w: refused", onboard.ErrProbe)
		}, http.StatusBadGateway, `"error":"the probe proved nothing: refused"`},
		{"vendor type", func(context.Context, string) (onboard.Result, error) {
			return onboard.Result{}, fmt.Errorf("x: %w", onboard.ErrNotOnboarded)
		}, http.StatusUnprocessableEntity, "generic"},
		{"administrator state", func(context.Context, string) (onboard.Result, error) {
			return onboard.Result{}, fmt.Errorf("x: %w", onboard.ErrAdministratorState)
		}, http.StatusConflict, "administrator"},
		{"no such device", func(context.Context, string) (onboard.Result, error) {
			return onboard.Result{}, inventory.ErrItemNotFound
		}, http.StatusNotFound, "not found"},
		{"a broken store", func(context.Context, string) (onboard.Result, error) {
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
