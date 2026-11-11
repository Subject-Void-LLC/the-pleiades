// This file holds PATCH /inventories/{id} to the partial-update rules the
// template, access, project and credential PATCHes follow, through the real
// router and the real ent-backed set store.
//
// The route used to decode its create body, so a name or description the
// body left out was written back empty: a PATCH naming only the devices was
// refused for the blank name with a 500, and one naming only the name erased
// the description. A null membership list read as absent and answered 200,
// and a blank name answered 500 on create and on update alike.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// inventoryFixtureSeq names each fixture's in-memory database, so parallel
// tests never share one.
var inventoryFixtureSeq atomic.Int64

// inventoryFixture is the real router over the real ent-backed set store,
// with one organization, one of its devices and one group of its devices.
type inventoryFixture struct {
	client *ent.Client
	router http.Handler

	orgID    int
	deviceID int
	groupID  int
}

// newInventoryFixture wires the inventory routes over a fresh database.
func newInventoryFixture(t *testing.T) *inventoryFixture {
	t.Helper()

	client := newSerializedSQLiteClient(t, fmt.Sprintf("inventories-%d", inventoryFixtureSeq.Add(1)))
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	device := client.Device.Create().SetName("edge-1").SetType("linux_server").SetOrganizationID(org.ID).SaveX(ctx)
	other := client.Device.Create().SetName("edge-2").SetType("linux_server").SetOrganizationID(org.ID).SaveX(ctx)
	group := client.Group.Create().SetName("edges").AddDeviceIDs(other.ID).SaveX(ctx)

	handler := api.NewInventoryHandler(inventory.NewEntSetStore(client), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.GetInventory.Route(handler.Get),
			apispec.CreateInventory.Route(handler.Create),
			apispec.UpdateInventory.Route(handler.Update),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return &inventoryFixture{client: client, router: router, orgID: org.ID, deviceID: device.ID, groupID: group.ID}
}

// do sends one request through the router with body as its raw JSON.
func (f *inventoryFixture) do(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	return serveRaw(t, f.router, method, path, body)
}

// serveRaw sends one request through router with body as its raw bytes,
// and returns the status and the response body. An empty body sends none.
func serveRaw(t *testing.T, router http.Handler, method, path, body string) (int, []byte) {
	t.Helper()
	rec := serveWith(t, router, method, path, body, nil)
	return rec.Code, rec.Body.Bytes()
}

// serveWith sends one request through router with body as its raw bytes and
// header's values added, and returns the recorded response.
func serveWith(t *testing.T, router http.Handler, method, path, body string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// inventoryPath is the URL of one inventory.
func inventoryPath(id int) string { return fmt.Sprintf("/api/v1/inventories/%d", id) }

// etag GETs an inventory and returns the ETag it answered with.
func (f *inventoryFixture) etag(t *testing.T, id int) string {
	t.Helper()
	rec := serveWith(t, f.router, http.MethodGet, inventoryPath(id), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET inventory %d: status %d, body %s", id, rec.Code, rec.Body.Bytes())
	}
	tag := rec.Header().Get("ETag")
	if tag == "" {
		t.Fatalf("GET inventory %d answered with no ETag", id)
	}
	return tag
}

// patchIfMatch sends a PATCH carrying If-Match: ifMatch, and returns the
// recorded response.
func (f *inventoryFixture) patchIfMatch(t *testing.T, id int, body, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	return serveWith(t, f.router, http.MethodPatch, inventoryPath(id), body, http.Header{"If-Match": {ifMatch}})
}

// patchCurrent sends a PATCH carrying the inventory's current ETag, as a
// client that has just read it does.
func (f *inventoryFixture) patchCurrent(t *testing.T, id int, body string) (int, []byte) {
	t.Helper()
	rec := f.patchIfMatch(t, id, body, f.etag(t, id))
	return rec.Code, rec.Body.Bytes()
}

// create posts an inventory that must be accepted and returns its id.
func (f *inventoryFixture) create(t *testing.T) int {
	t.Helper()
	body := fmt.Sprintf(`{"name":"edge","description":"Edge routers","organization":%d,"groups":[%d],"devices":[%d]}`,
		f.orgID, f.groupID, f.deviceID)
	status, resp := f.do(t, http.MethodPost, "/api/v1/inventories", body)
	if status != http.StatusCreated {
		t.Fatalf("POST %s: status %d, body %s", body, status, resp)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil {
		t.Fatalf("decoding the created inventory: %v", err)
	}
	return created.ID
}

// read GETs an inventory and returns its decoded body.
func (f *inventoryFixture) read(t *testing.T, id int) map[string]any {
	t.Helper()
	status, body := f.do(t, http.MethodGet, inventoryPath(id), "")
	if status != http.StatusOK {
		t.Fatalf("GET inventory %d: status %d, body %s", id, status, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding inventory %d: %v", id, err)
	}
	delete(got, "_links")
	return got
}

// patchOK sends a PATCH with the current ETag that must succeed.
func (f *inventoryFixture) patchOK(t *testing.T, id int, body string) {
	t.Helper()
	if status, resp := f.patchCurrent(t, id, body); status != http.StatusOK {
		t.Fatalf("PATCH %s: status %d, want 200; body %s", body, status, resp)
	}
}

// refusedUnchanged sends a PATCH with the current ETag that must be refused
// with want, with a response saying each of mentions, and leave the
// inventory as it was.
func (f *inventoryFixture) refusedUnchanged(t *testing.T, id int, body string, want int, mentions ...string) {
	t.Helper()
	before := f.read(t, id)
	status, resp := f.patchCurrent(t, id, body)
	if status != want {
		t.Fatalf("PATCH %s: status %d, want %d; body %s", body, status, want, resp)
	}
	for _, mention := range mentions {
		if !strings.Contains(string(resp), mention) {
			t.Errorf("PATCH %s: the refusal %s does not say %q", body, resp, mention)
		}
	}
	if after := f.read(t, id); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused PATCH changed the inventory:\n before %v\n after  %v", before, after)
	}
}

// ids is the JSON form of a member list as GET decodes it.
func ids(values ...int) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, float64(v))
	}
	return out
}

// TestInventoryPatch_KeepsWhatItDoesNotName is the partial update: a PATCH
// naming one field leaves every other field and both member lists as stored.
func TestInventoryPatch_KeepsWhatItDoesNotName(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)

	f.patchOK(t, id, `{"name":"edge-routers"}`)
	assertProjectFields(t, f.read(t, id), map[string]any{
		"name": "edge-routers", "description": "Edge routers",
		"groups": ids(f.groupID), "devices": ids(f.deviceID),
	})

	f.patchOK(t, id, `{"devices":[]}`)
	assertProjectFields(t, f.read(t, id), map[string]any{
		"name": "edge-routers", "description": "Edge routers",
		"groups": ids(f.groupID), "devices": ids(),
	})
}

// TestInventoryPatch_AnEmptyValueClears is the other half: a field the body
// names is applied, and an empty value clears what may be empty.
func TestInventoryPatch_AnEmptyValueClears(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)

	f.patchOK(t, id, `{"description":"","groups":[]}`)
	assertProjectFields(t, f.read(t, id), map[string]any{
		"name": "edge", "groups": ids(), "devices": ids(f.deviceID),
	}, "description")
}

// TestInventoryPatch_RefusesWhatItCannotApply covers every refusal: a null,
// which a pointer alone reads as absent, a blank name, an unknown or
// read-only key, a field named twice, a body that is not an object, and an
// organization other than the stored one. Each leaves the inventory as it
// was.
func TestInventoryPatch_RefusesWhatItCannotApply(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)

	cases := map[string]struct {
		body     string
		want     int
		mentions []string
	}{
		"a null name":           {`{"name":null}`, http.StatusBadRequest, []string{`\"name\"`, "null"}},
		"a null description":    {`{"description":null}`, http.StatusBadRequest, []string{`\"description\"`}},
		"a null device list":    {`{"devices":null}`, http.StatusBadRequest, []string{`\"devices\"`}},
		"a null group list":     {`{"groups":null,"name":"x"}`, http.StatusBadRequest, []string{`\"groups\"`}},
		"an empty name":         {`{"name":""}`, http.StatusBadRequest, []string{"name"}},
		"a blank name":          {`{"name":"   "}`, http.StatusBadRequest, []string{"name"}},
		"the owner":             {`{"owner":"mallory"}`, http.StatusBadRequest, nil},
		"a read-only count":     {`{"device_count":4}`, http.StatusBadRequest, nil},
		"a field named twice":   {`{"name":"a","NAME":"b"}`, http.StatusBadRequest, []string{`\"name\"`, "more than once"}},
		"a bare null":           {`null`, http.StatusBadRequest, nil},
		"an array":              {`[]`, http.StatusBadRequest, nil},
		"another organization":  {fmt.Sprintf(`{"organization":%d}`, f.orgID+1), http.StatusBadRequest, []string{"organization cannot be changed"}},
		"an unknown device":     {`{"devices":[999999]}`, http.StatusBadRequest, []string{"device"}},
		"an unknown group":      {`{"groups":[999999]}`, http.StatusBadRequest, []string{"group"}},
		"a negative device id":  {`{"devices":[-1]}`, http.StatusBadRequest, nil},
		"a string for the name": {`{"name":7}`, http.StatusBadRequest, nil},
		"a NUL in the name":     {`{"name":"a\u0000b"}`, http.StatusBadRequest, []string{"name may not contain a NUL"}},
		"a NUL in the text":     {`{"description":"\u0000"}`, http.StatusBadRequest, []string{"description may not contain a NUL"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f.refusedUnchanged(t, id, tc.body, tc.want, tc.mentions...)
		})
	}
}

// TestInventoryPatch_AcceptsTheStoredOrganizationAsAnEcho is the fixed
// field's other half: sending back what GET showed is not a move.
func TestInventoryPatch_AcceptsTheStoredOrganizationAsAnEcho(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)

	f.patchOK(t, id, fmt.Sprintf(`{"organization":%d,"description":"Core"}`, f.orgID))
	assertProjectFields(t, f.read(t, id), map[string]any{
		"organization": float64(f.orgID), "description": "Core", "name": "edge",
	})
}

// TestInventoryPatch_ATakenNameIsAConflict keeps the store's 409 for a name
// another inventory of the organization holds.
func TestInventoryPatch_ATakenNameIsAConflict(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	id := f.create(t)
	other := fmt.Sprintf(`{"name":"core","organization":%d}`, f.orgID)
	if status, resp := f.do(t, http.MethodPost, "/api/v1/inventories", other); status != http.StatusCreated {
		t.Fatalf("POST %s: status %d, body %s", other, status, resp)
	}

	f.refusedUnchanged(t, id, `{"name":"core"}`, http.StatusConflict, "already exists")
}

// TestInventoryPatch_AMissingInventoryIsNotFound answers 404 before the body
// is applied to anything.
func TestInventoryPatch_AMissingInventoryIsNotFound(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	if status, resp := f.do(t, http.MethodPatch, inventoryPath(4242), `{"name":"x"}`); status != http.StatusNotFound {
		t.Fatalf("PATCH a missing inventory: status %d, want 404; body %s", status, resp)
	}
}

// TestInventoryCreate_RefusesWhatTheStoreCannotSave is the create route's
// share of the same 500s: a blank name, no organization, an organization
// that does not exist and a member that does not exist are the caller's
// mistakes, answered 400, never a 500 or a 409 claiming the name is taken.
func TestInventoryCreate_RefusesWhatTheStoreCannotSave(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)

	cases := map[string]struct {
		body     string
		mentions []string
	}{
		"an empty name":            {fmt.Sprintf(`{"name":"","organization":%d}`, f.orgID), []string{"name"}},
		"a blank name":             {fmt.Sprintf(`{"name":" \t ","organization":%d}`, f.orgID), []string{"name"}},
		"no organization":          {`{"name":"edge"}`, []string{"organization"}},
		"an unknown organization":  {`{"name":"edge","organization":999999}`, []string{"organization"}},
		"an unknown device":        {fmt.Sprintf(`{"name":"edge","organization":%d,"devices":[999999]}`, f.orgID), []string{"device"}},
		"an unknown group":         {fmt.Sprintf(`{"name":"edge","organization":%d,"groups":[999999]}`, f.orgID), []string{"group"}},
		"a NUL in the name":        {fmt.Sprintf(`{"name":"a\u0000","organization":%d}`, f.orgID), []string{"name may not contain a NUL"}},
		"a NUL in the description": {fmt.Sprintf(`{"name":"edge","description":"\u0000","organization":%d}`, f.orgID), []string{"description may not contain a NUL"}},
		"an unknown key on create": {fmt.Sprintf(`{"name":"edge","organization":%d,"owner":"mallory"}`, f.orgID), nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			status, resp := f.do(t, http.MethodPost, "/api/v1/inventories", tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("POST %s: status %d, want 400; body %s", tc.body, status, resp)
			}
			for _, mention := range tc.mentions {
				if !strings.Contains(string(resp), mention) {
					t.Errorf("POST %s: the refusal %s does not say %q", tc.body, resp, mention)
				}
			}
		})
	}
	if n := f.client.Inventory.Query().CountX(context.Background()); n != 0 {
		t.Errorf("refused creates stored %d inventories, want 0", n)
	}
}

// TestInventoryMembers_ForeignAnswersLikeUnknown is the owner's decision of
// 2026-11-10: a member that belongs to another organization and one that
// does not exist get the same answer, byte for byte, on create and on PATCH,
// so probing ids one at a time no longer maps another tenant's fleet. It used
// to be 403 for the foreign one and 400 for the unknown one.
func TestInventoryMembers_ForeignAnswersLikeUnknown(t *testing.T) {
	t.Parallel()
	f := newInventoryFixture(t)
	ctx := context.Background()
	other := f.client.Organization.Create().SetName("globex").SaveX(ctx)
	foreign := f.client.Device.Create().SetName("their-rtr").SetType("linux_server").SetOrganizationID(other.ID).SaveX(ctx)
	foreignGroup := f.client.Group.Create().SetName("theirs").AddDeviceIDs(foreign.ID).SaveX(ctx)
	id := f.create(t)

	answer := func(method, path, body string) string {
		t.Helper()
		status, resp := f.do(t, method, path, body)
		return fmt.Sprintf("%d %s", status, resp)
	}
	// patched sends the PATCH with the current ETag, which a membership
	// PATCH needs, so the answer is the member refusal and not the 428.
	patched := func(body string) string {
		t.Helper()
		status, resp := f.patchCurrent(t, id, body)
		return fmt.Sprintf("%d %s", status, resp)
	}
	pairs := map[string][2]string{
		"create, a device": {
			answer(http.MethodPost, "/api/v1/inventories", fmt.Sprintf(`{"name":"a","organization":%d,"devices":[%d]}`, f.orgID, foreign.ID)),
			answer(http.MethodPost, "/api/v1/inventories", fmt.Sprintf(`{"name":"a","organization":%d,"devices":[999999]}`, f.orgID)),
		},
		"patch, a group": {
			patched(fmt.Sprintf(`{"groups":[%d]}`, foreignGroup.ID)),
			patched(`{"groups":[999999]}`),
		},
	}
	for name, pair := range pairs {
		if pair[0] != pair[1] {
			t.Errorf("%s: a foreign member answered %q, an unknown one %q; want the same", name, pair[0], pair[1])
		}
		if !strings.HasPrefix(pair[0], "400 ") || !strings.Contains(pair[0], "cannot be added to this inventory") {
			t.Errorf("%s: answer %q, want the one 400", name, pair[0])
		}
	}
}
