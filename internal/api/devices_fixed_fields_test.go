// Package api_test: a device update refuses to change what it cannot change,
// a device's name and type, rather than answering 200 and ignoring them.
package api_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// TestDeviceUpdate_ANameOrTypeChangeIsRefusedAndAnEchoIsAccepted runs the real
// handler over the real ent repository. A PATCH naming another name or type
// used to answer 200 and store neither, since the handler never read them;
// a client renaming a device was told it had. Each is now refused with 400
// naming the field, and nothing is stored. A body that repeats the stored
// name and type, as a client echoing the record back sends, still applies
// its other changes.
func TestDeviceUpdate_ANameOrTypeChangeIsRefusedAndAnEchoIsAccepted(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()
	client.Device.Create().SetName("web1").SetType("linux_server").SaveX(ctx)
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

	for _, tc := range []struct{ body, field string }{
		{`{"name": "web2", "tags": ["moved"]}`, "name"},
		{`{"type": "cisco_router", "tags": ["moved"]}`, "type"},
	} {
		rec := patch(tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s = %d, want 400: %s", tc.body, rec.Code, rec.Body)
			continue
		}
		if !strings.Contains(rec.Body.String(), tc.field) {
			t.Errorf("PATCH %s refused with %s, want the reason to name %q", tc.body, rec.Body, tc.field)
		}
	}
	stored, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version() != before.Version() || len(stored.Tags()) != 0 {
		t.Fatalf("the refused PATCHes stored version %d with tags %v, want version %d unchanged",
			stored.Version(), stored.Tags(), before.Version())
	}
	if _, err := repo.GetByName(ctx, "web2"); err == nil {
		t.Fatal("a refused rename stored a device named web2")
	}

	echo := `{"name": "web1", "type": "linux_server", "tags": ["echoed"]}`
	if rec := patch(echo); rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s = %d, want 200: %s", echo, rec.Code, rec.Body)
	}
	if stored, err = repo.GetByName(ctx, "web1"); err != nil {
		t.Fatal(err)
	}
	if got := stored.Tags(); len(got) != 1 || got[0] != "echoed" {
		t.Errorf("after the echoing PATCH the device has tags %v, want [echoed]", got)
	}
}
