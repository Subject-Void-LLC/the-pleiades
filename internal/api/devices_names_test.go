// Package api_test: the API refuses to create a device whose name no URL can
// carry, saying why, through the real request pipeline. The handler's own
// refusal and the store's are each pinned on their own.
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
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// unreachableStore is a device repository whose Create fails the test: the
// handler must refuse before it asks the store anything.
type unreachableStore struct {
	*stubDeviceRepo
	t *testing.T
}

// Create implements api.DeviceRepository, failing the test.
func (s unreachableStore) Create(_ context.Context, item pkginventory.InventoryItem) error {
	s.t.Errorf("the handler asked the store to create %q; its own guard should have refused it", item.Name())
	return nil
}

// createRouter is POST /inventory/devices over repo, through api.NewRouter,
// real JWT authentication, a real admission chain and a real generator. It
// returns the router and a function posting one device as readWriteIdentity.
func createRouter(t *testing.T, repo api.DeviceRepository) func(name string) *httptest.ResponseRecorder {
	t.Helper()
	issuer := authtest.New(t, "devicenames-issuer", "devicenames-audience")
	chain := auth.AdmissionChain{auth.NewTokenScopeRule(issuer.Evaluator())}
	generator, err := auth.NewAdmissionHATEOASGenerator(chain)
	if err != nil {
		t.Fatalf("building generator: %v", err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	devices := api.NewDeviceHandler(repo, inventory.NewItemFactory(), logger)
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    logger,
		Auth:      api.AuthMiddleware(issuer.Evaluator()),
		Admission: auth.Admission{Chain: chain},
		HATEOAS:   generator,
		Routes: []api.Route{
			{Method: http.MethodPost, Pattern: "/inventory/devices", Scope: auth.ScopeInventoryWrite, Rel: auth.RelCreate, Handler: devices.Create},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return func(name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"name": name, "type": "linux_server"})
		req := httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/inventory/devices", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", issuer.BearerToken(t, readWriteIdentity))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}
}

// errorMessage decodes a response's {"error": ...} body.
func errorMessage(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %q", rr.Body.String())
	}
	return body.Error
}

// TestDeviceCreate_RefusesANameNoURLCarries posts a device named "", "."
// and ".." and expects each refused by the handler itself, with 400 and the
// reason, before the store is asked anything.
func TestDeviceCreate_RefusesANameNoURLCarries(t *testing.T) {
	post := createRouter(t, unreachableStore{stubDeviceRepo: &stubDeviceRepo{}, t: t})
	for _, tc := range []struct{ name, says string }{
		{"", "a name is required"},
		{".", `the name "." cannot be used`},
		{"..", `the name ".." cannot be used`},
	} {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			rr := post(tc.name)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("POST a device named %q = %d, want 400: %s", tc.name, rr.Code, rr.Body.String())
			}
			if msg := errorMessage(t, rr); !strings.Contains(msg, tc.says) {
				t.Errorf("the refusal says %q, want it to say %q", msg, tc.says)
			}
		})
	}
}

// TestDeviceCreate_MapsTheStoresNameRefusalTo400: a store that refuses a
// name the handler let through (here one it would have accepted, so only
// the store can be refusing) is answered 400 with recordname's reason,
// never 500.
func TestDeviceCreate_MapsTheStoresNameRefusalTo400(t *testing.T) {
	refusal := fmt.Errorf("creating device: %w", recordname.Check(".."))
	post := createRouter(t, &stubDeviceRepo{createErr: refusal})
	rr := post("edge-01")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status is %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if msg := errorMessage(t, rr); !strings.Contains(msg, `the name ".." cannot be used`) {
		t.Errorf("the body says %q, want recordname's reason", msg)
	}
}
