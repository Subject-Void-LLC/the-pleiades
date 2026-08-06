package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/api"
	"github.com/SubjectVoidLLC/the-pleiades/internal/auth"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// TestDispatcher_GetGroupSelector_FiltersAgainstRealRepository is the real
// end-to-end proof of this phase's fix, run against the actual
// entRepository/entIterator code path (RULE 0), not the hand-written
// MockRepository the rest of this package's tests use. Before this
// phase, DispatchRunbook's "group" query parameter was silently discarded
// by GetGroup, so every dispatch hit the whole devices table regardless
// of which group a caller asked for. Two named ent Groups are seeded with
// distinct devices; one HTTP request per group must dispatch to exactly
// that group's devices, and a third request naming a group with no real
// members must dispatch to zero devices (fail closed), never fall back to
// the whole fleet.
func TestDispatcher_GetGroupSelector_FiltersAgainstRealRepository(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	mkDevice := func(name string) *ent.Device {
		return client.Device.Create().
			SetName(name).
			SetType("linux_server").
			SetProperties(map[string]interface{}{"ip": "10.0.0.1"}).
			SaveX(ctx)
	}

	prod1, prod2 := mkDevice("prod-1"), mkDevice("prod-2")
	staging1 := mkDevice("staging-1")

	client.Group.Create().SetName("prod-real").AddDevices(prod1, prod2).SaveX(ctx)
	client.Group.Create().SetName("staging-real").AddDevices(staging1).SaveX(ctx)

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	bus := &mockBus{}
	dispatcher := api.NewDispatcher(repo, bus)

	dispatchGroup := func(t *testing.T, groupName string) int {
		t.Helper()
		bus.Publishes = 0
		req := httptest.NewRequest("POST", "/dispatch?group="+groupName+"&runbook=pb-1", nil)
		reqCtx := context.WithValue(req.Context(), api.IdentityKeyForTest, &auth.Identity{Subject: "user"})
		req = req.WithContext(reqCtx)

		rr := httptest.NewRecorder()
		dispatcher.DispatchRunbook(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("dispatch to %q: expected 200 OK, got %v", groupName, rr.Code)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
		return int(resp["dispatched"].(float64))
	}

	if got := dispatchGroup(t, "prod-real"); got != 2 {
		t.Errorf("dispatch to group 'prod-real' reached %d devices, want 2", got)
	}
	if bus.Publishes != 2 {
		t.Errorf("expected 2 publishes for group 'prod-real', got %d", bus.Publishes)
	}

	if got := dispatchGroup(t, "staging-real"); got != 1 {
		t.Errorf("dispatch to group 'staging-real' reached %d devices, want 1", got)
	}

	// A group with zero real members (including one that does not exist
	// at all) must dispatch to zero devices, not fall back to the whole
	// fleet -- the literal bug this phase fixed.
	if got := dispatchGroup(t, "does-not-exist"); got != 0 {
		t.Errorf("dispatch to a nonexistent group reached %d devices, want 0 (fail closed)", got)
	}
}
