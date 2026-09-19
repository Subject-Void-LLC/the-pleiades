// Package api_test: the device update route against a real ent repository.
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

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestDeviceUpdate_AStateOrTagsOnlyChangeIsStored is the regression test
// for a device update that answered 200 and stored nothing. The handler
// rebuilds the device at the version it loaded, and both repositories'
// Save treat an unchanged version as nothing to write, so a PATCH that
// changed only the lifecycle state or the tags, which is the only way to
// promote a device out of quarantine or simulate-locked, was dropped. It
// runs the real handler, the real ent repository and the real factory the
// Controller wires, and reads the device back from the database rather
// than trusting the response.
func TestDeviceUpdate_AStateOrTagsOnlyChangeIsStored(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()
	client.Device.Create().
		SetName("web1").
		SetType("linux_server").
		SetProperties(map[string]interface{}{"host": "10.0.0.1"}).
		SetState(pkginventory.StateSimulateLocked.String()).
		SaveX(ctx)
	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	before, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	handler := api.NewDeviceHandler(repo, inventory.NewItemFactory(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	router := chi.NewRouter()
	router.Patch("/inventory/devices/{name}", handler.Update)

	patch := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/inventory/devices/web1", strings.NewReader(body)))
		return rec
	}

	rec := patch(`{"state": "active"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH state = %d: %s", rec.Code, rec.Body)
	}
	var dto struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.State() != pkginventory.StateActive || dto.State != "active" {
		t.Errorf("after PATCH {state: active} the device is stored as %s and reported as %q, want active both ways", stored.State(), dto.State)
	}
	if stored.Version() != before.Version()+1 {
		t.Errorf("the state change took the device from version %d to %d, want one revision", before.Version(), stored.Version())
	}
	if h := stored.History(); len(h) == 0 || h[len(h)-1].Field != "state" ||
		h[len(h)-1].OldValue != "simulate-locked" || h[len(h)-1].NewValue != "active" {
		t.Errorf("the stored history ends %+v, want a state revision from simulate-locked to active", h)
	}

	if rec := patch(`{"tags": ["web", "prod"]}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH tags = %d: %s", rec.Code, rec.Body)
	}
	if stored, err = repo.GetByName(ctx, "web1"); err != nil {
		t.Fatal(err)
	}
	if got := stored.Tags(); len(got) != 2 {
		t.Errorf("after PATCH {tags: [web, prod]} the device is stored with tags %v", got)
	}
}
