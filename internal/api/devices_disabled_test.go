package api_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// writeDisabledRouter mounts the device handlers with no factory, the
// configuration a composition root that wires only the read path produces.
// Create and Update refuse rather than panicking, so that stays a
// supported configuration instead of a latent nil dereference.
func writeDisabledRouter(t *testing.T, repo api.DeviceRepository) http.Handler {
	t.Helper()
	handler := api.NewDeviceHandler(repo, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodPost, Pattern: "/inventory/devices", Scope: auth.ScopeInventoryWrite, Rel: auth.RelCreate, Handler: handler.Create},
			{Method: http.MethodPatch, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelUpdate, Handler: handler.Update},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestDeviceHandler_WritesRefuseWithoutAFactory(t *testing.T) {
	repo := &stubDeviceRepo{item: testDevice(t, "id-1", "router-1")}
	router := writeDisabledRouter(t, repo)

	for _, tc := range []struct {
		name   string
		method string
		target string
	}{
		{"create", http.MethodPost, "/api/v1/inventory/devices"},
		{"update", http.MethodPatch, "/api/v1/inventory/devices/router-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, router, tc.method, tc.target, `{"name":"router-1","type":"linux_server"}`)
			if rec.Code != http.StatusNotImplemented {
				t.Errorf("status = %d, want 501", rec.Code)
			}
			if repo.created != nil || repo.saved != nil {
				t.Error("a write reached the repository with no factory wired")
			}
		})
	}
}

// A failure surfaced by the iterator rather than by GetGroup itself is
// still a failed request: the alternative is answering 200 with a
// truncated page and no indication that anything went wrong.
func TestDeviceHandler_ListReportsAnIteratorFailure(t *testing.T) {
	router := deviceRouter(t, &failingIteratorRepo{})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventory/devices", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

type failingIteratorRepo struct{ stubDeviceRepo }

func (r *failingIteratorRepo) GetGroup(context.Context, pkginventory.Selector) (inventory.Iterator, error) {
	return &erroringIterator{}, nil
}

type erroringIterator struct{}

func (i *erroringIterator) Next(context.Context) bool        { return false }
func (i *erroringIterator) Item() pkginventory.InventoryItem { return nil }
func (i *erroringIterator) Error() error                     { return errors.New("cursor died mid-stream") }
func (i *erroringIterator) Close() error                     { return nil }

// A stored item that cannot report its own classification cannot be
// rebuilt through the factory, so the update is refused rather than
// written with a guessed type. inventorytest.Stub carries no DeviceType,
// which is exactly the shape this guards against.
func TestDeviceHandler_UpdateRefusesAnItemWithNoDeviceType(t *testing.T) {
	repo := &stubDeviceRepo{item: &inventorytest.Stub{
		StubName:  "router-1",
		StubState: pkginventory.StateActive,
	}}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1", `{"tags":["x"]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if repo.saved != nil {
		t.Error("an unclassifiable item reached the repository")
	}
}
