// Package api_test: the API refuses to create a device whose name no URL can
// carry, saying why, through the real request pipeline.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// TestDeviceCreate_RefusesANameNoURLCarries posts a device named "", "."
// and ".." through api.NewRouter, real JWT authentication, a real admission
// chain and a real ent repository, and expects each refused with 400 and the
// reason, and nothing stored. Three dots, a near miss, is created.
func TestDeviceCreate_RefusesANameNoURLCarries(t *testing.T) {
	dsn := fmt.Sprintf("file:devicenames-%d?mode=memory&cache=shared&_fk=1", pathFixtureSeq.Add(1))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
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
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: devices.Get},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	post := func(name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"name": name, "type": "linux_server"})
		req := httptest.NewRequest(http.MethodPost, api.APIVersionPrefix+"/inventory/devices", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", issuer.BearerToken(t, readWriteIdentity))
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

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
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %q", rr.Body.String())
			}
			if !strings.Contains(body.Error, tc.says) {
				t.Errorf("the refusal says %q, want it to say %q", body.Error, tc.says)
			}
			if _, err := repo.GetByName(context.Background(), tc.name); !errors.Is(err, inventory.ErrItemNotFound) {
				t.Errorf("a device named %q was stored (GetByName = %v)", tc.name, err)
			}
		})
	}

	if rr := post("..."); rr.Code != http.StatusCreated {
		t.Fatalf("POST a device named \"...\" = %d, want 201: %s", rr.Code, rr.Body.String())
	}
}

// TestDeviceHandler_MapsTheStoresNameRefusalTo400 covers the mapping for a
// store refusal that reaches the handler some other way than a create's own
// check: a 400 carrying recordname's reason, never a 500.
func TestDeviceHandler_MapsTheStoresNameRefusalTo400(t *testing.T) {
	refusal := fmt.Errorf("creating device: %w", recordname.Check(".."))
	router := deviceRouter(t, &stubDeviceRepo{getErr: refusal})

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/edge-01", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status is %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "cannot be used") {
		t.Errorf("the body %s does not carry the reason", rr.Body.String())
	}
}
