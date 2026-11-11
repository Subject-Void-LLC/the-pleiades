// This file holds PATCH /inventories/{id}'s If-Match precondition and the
// inventory's ETag to their contract, through the real router and the real
// ent-backed set store.
//
// A PATCH naming groups or devices replaces that list whole. Without a
// version check, a client that read an inventory before another removed a
// device from it (revoking the device from every team the inventory is
// shared with) put the device back by sending the list it had read.
package api_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// TestInventoryETag_ComesWithEveryReadAndWrite is the tag a client sends
// back: version 0 on the create, the same tag on a read, and version 1 with
// the PATCH's own tag after one PATCH.
func TestInventoryETag_ComesWithEveryReadAndWrite(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	body := fmt.Sprintf(`{"name":"core","organization":%d}`, f.orgID)
	created := serveWith(t, f.router, http.MethodPost, "/api/v1/inventories", body, nil)
	if tag := created.Header().Get("ETag"); created.Code != http.StatusCreated || !strings.HasPrefix(tag, `"0.`) {
		t.Fatalf("POST: status %d ETag %q, want 201 and a version 0 tag", created.Code, tag)
	}
	id := f.create(t)
	read := f.etag(t, id)
	if !strings.HasPrefix(read, `"0.`) {
		t.Errorf("GET ETag = %q, want a version 0 tag", read)
	}
	patched := f.patchIfMatch(t, id, fmt.Sprintf(`{"devices":[%d]}`, f.deviceID), read)
	tag := patched.Header().Get("ETag")
	if patched.Code != http.StatusOK || !strings.HasPrefix(tag, `"1.`) {
		t.Fatalf("PATCH: status %d ETag %q, want 200 and a version 1 tag; body %s", patched.Code, tag, patched.Body.Bytes())
	}
	if got := f.etag(t, id); got != tag {
		t.Errorf("GET ETag after a PATCH = %q, want the PATCH's %q", got, tag)
	}
}

// TestInventoryPatch_AWriteThatLeavesTheVersionStillMovesTheTag is a write
// from a build that predates the version column, which keeps serving the
// migrated schema during a rolling upgrade or after a rollback: it removes a
// device and leaves the version as it was. The tag still moves, since it
// digests what the inventory holds, so a client holding the old tag cannot
// put the device back.
func TestInventoryPatch_AWriteThatLeavesTheVersionStillMovesTheTag(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)
	read := f.etag(t, id)

	// The older build's write: the same row update with no version change.
	f.client.Inventory.UpdateOneID(id).ClearDevices().ExecX(context.Background())
	if got := f.etag(t, id); got == read || !strings.HasPrefix(got, `"0.`) {
		t.Fatalf("after an unversioned write the tag is %q (read %q), want a different version 0 tag", got, read)
	}
	rec := f.patchIfMatch(t, id, fmt.Sprintf(`{"devices":[%d]}`, f.deviceID), read)
	if rec.Code != http.StatusPreconditionFailed {
		t.Errorf("PATCH with the tag read before it: status %d body %s, want 412", rec.Code, rec.Body.Bytes())
	}
}

// commitFirst is the real set store, except that its first UpdateWith lets
// another update commit before it takes the row's lock: the window between a
// client's read and the PATCH's write, made certain rather than raced.
type commitFirst struct {
	api.InventoryRepository
	other func()
	once  sync.Once
}

// UpdateWith runs the other update once, then the real UpdateWith.
func (c *commitFirst) UpdateWith(ctx context.Context, id int, fold func(inventory.Set) (inventory.Set, error)) (inventory.Set, error) {
	c.once.Do(c.other)
	return c.InventoryRepository.UpdateWith(ctx, id, fold)
}

// TestInventoryPatch_IfMatchIsJudgedUnderTheLock: an update commits after the
// PATCH's request arrives and before the store locks the row. The PATCH,
// carrying the tag from before that update, is refused. A handler that read
// the inventory and compared the tag before handing the store a plain fold
// would pass every other test here and write the stale list.
func TestInventoryPatch_IfMatchIsJudgedUnderTheLock(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)
	read := f.etag(t, id)

	store := inventory.NewEntSetStore(f.client)
	repo := &commitFirst{InventoryRepository: store, other: func() {
		if _, err := store.UpdateWith(context.Background(), id, func(s inventory.Set) (inventory.Set, error) {
			s.DeviceIDs = nil
			return s, nil
		}); err != nil {
			t.Errorf("the other update: %v", err)
		}
	}}
	handler := api.NewInventoryHandler(repo, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.UpdateInventory.Route(handler.Update)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	rec := serveWith(t, router, http.MethodPatch, inventoryPath(id), fmt.Sprintf(`{"devices":[%d]}`, f.deviceID), http.Header{"If-Match": {read}})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("PATCH racing a committed update: status %d body %s, want 412", rec.Code, rec.Body.Bytes())
	}
	if got := f.read(t, id); len(got["devices"].([]any)) != 0 {
		t.Errorf("the stale PATCH wrote its list: %v", got["devices"])
	}
}

// TestInventoryPatch_NamingAMemberListNeedsIfMatch is the 428: a PATCH that
// names either list and sends no If-Match is refused and changes nothing,
// while one naming neither list needs none.
func TestInventoryPatch_NamingAMemberListNeedsIfMatch(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)
	for _, body := range []string{`{"devices":[]}`, `{"groups":[]}`, `{"name":"edge-2","devices":[]}`} {
		before := f.read(t, id)
		status, resp := f.do(t, http.MethodPatch, inventoryPath(id), body)
		if status != http.StatusPreconditionRequired || !strings.Contains(string(resp), "If-Match") {
			t.Errorf("PATCH %s with no If-Match: status %d body %s, want 428 naming If-Match", body, status, resp)
		}
		if after := f.read(t, id); fmt.Sprint(after) != fmt.Sprint(before) {
			t.Errorf("PATCH %s with no If-Match changed the inventory: %v to %v", body, before, after)
		}
	}
	if status, resp := f.do(t, http.MethodPatch, inventoryPath(id), `{"name":"renamed","description":"d"}`); status != http.StatusOK {
		t.Errorf("a PATCH naming no list, with no If-Match: status %d body %s, want 200", status, resp)
	}
}

// TestInventoryPatch_AStaleCopyCannotRevertARevocation is the lost update
// itself. Two clients read the inventory at one version. The first removes a
// device; the second, still holding the old list, sends it back with the tag
// it read. The second is refused and the device stays removed.
func TestInventoryPatch_AStaleCopyCannotRevertARevocation(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)
	read := f.etag(t, id)

	if rec := f.patchIfMatch(t, id, `{"devices":[]}`, read); rec.Code != http.StatusOK {
		t.Fatalf("the revoking PATCH: status %d body %s", rec.Code, rec.Body.Bytes())
	}
	stale := f.patchIfMatch(t, id, fmt.Sprintf(`{"description":"typo fixed","devices":[%d]}`, f.deviceID), read)
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("the stale PATCH: status %d body %s, want 412", stale.Code, stale.Body.Bytes())
	}
	got := f.read(t, id)
	if devices, _ := got["devices"].([]any); len(devices) != 0 || got["description"] != "Edge routers" {
		t.Errorf("after the stale PATCH: %v, want no devices and the description untouched", got)
	}
}

// TestInventoryPatch_IfMatchMustNameTheCurrentTagExactly is every header that
// is not the current tag: each is a 412 with fixed text that repeats nothing
// sent, and changes nothing. A sent If-Match is checked on a PATCH naming no
// list too. "*" would match any inventory that exists, so it is refused.
func TestInventoryPatch_IfMatchMustNameTheCurrentTagExactly(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)
	stale := f.etag(t, id)
	f.patchOK(t, id, `{"description":"moved to version 1"}`)
	current := f.etag(t, id)
	bare := strings.Trim(current, `"`)
	version := bare[:strings.Index(bare, ".")]

	headers := map[string][]string{
		"a stale tag":          {stale},
		"a canary":             {`"zzz-canary"`},
		"a weak tag":           {"W/" + current},
		"a wildcard":           {`*`},
		"a list":               {current + ", " + current},
		"two header lines":     {current, current},
		"the version alone":    {`"` + version + `"`},
		"no quotes":            {bare},
		"an empty tag":         {`""`},
		"an empty header":      {``},
		"the tag and a canary": {`"` + bare + `zzz-canary"`},
		"another case":         {strings.ToUpper(current)},
	}
	for name, values := range headers {
		for _, body := range []string{`{"devices":[]}`, `{"name":"renamed"}`} {
			before := f.read(t, id)
			rec := serveWith(t, f.router, http.MethodPatch, inventoryPath(id), body, http.Header{"If-Match": values})
			if rec.Code != http.StatusPreconditionFailed {
				t.Errorf("%s, PATCH %s: status %d body %s, want 412", name, body, rec.Code, rec.Body.Bytes())
			}
			if strings.Contains(rec.Body.String(), "canary") {
				t.Errorf("%s: the refusal repeats the header: %s", name, rec.Body.Bytes())
			}
			if after := f.read(t, id); fmt.Sprint(after) != fmt.Sprint(before) {
				t.Errorf("%s, PATCH %s changed the inventory", name, body)
			}
		}
	}
	if rec := f.patchIfMatch(t, id, `{"devices":[]}`, " "+current+" "); rec.Code != http.StatusOK {
		t.Errorf("the current tag with surrounding space: status %d body %s, want 200", rec.Code, rec.Body.Bytes())
	}
}

// TestInventoryPatch_AMissingInventoryIsNotFoundBeforeAnyPrecondition: the
// 404 wins over both the 428 and the 412, so neither precondition answer
// says anything about an id that names no inventory.
func TestInventoryPatch_AMissingInventoryIsNotFoundBeforeAnyPrecondition(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	if status, resp := f.do(t, http.MethodPatch, inventoryPath(4242), `{"devices":[]}`); status != http.StatusNotFound {
		t.Errorf("no If-Match: status %d body %s, want 404", status, resp)
	}
	if rec := f.patchIfMatch(t, 4242, `{"devices":[]}`, `"zzz-canary"`); rec.Code != http.StatusNotFound {
		t.Errorf("a stale If-Match: status %d body %s, want 404", rec.Code, rec.Body.Bytes())
	}
}
