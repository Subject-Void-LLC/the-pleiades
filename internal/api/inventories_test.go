// This file covers InventoryHandler: the paging and search contract, the
// identity-derived owner, and every branch of the store-error mapping that
// decides whether a caller sees a 403, a 404, a 409, or a generic 500.
//
// The routes are built from apispec's own Endpoint values rather than
// hand-written api.Route literals, so a drift between the declared scope or
// pattern and what these tests exercise fails here rather than surviving to
// production. That is the same reason cmd/controller builds its table from
// apispec.Routes.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// stubSetStore is an in-memory api.InventoryRepository that records the
// query it was handed and can be programmed to fail one operation at a
// time.
//
// Recording the query is the only way to prove a parsed URL parameter
// actually reached the store rather than being validated and dropped, which
// is a real failure shape: the handler would still answer 200 with a full,
// unpaged result set.
type stubSetStore struct {
	sets   map[int]inventory.Set
	nextID int

	gotQuery inventory.SetQuery
	gotSet   inventory.Set

	getCalls    int
	getErrAfter int

	createErr error
	getErr    error
	listErr   error
	updateErr error
	deleteErr error
}

// newStubSetStore builds a store preloaded with sets, keyed by their ID.
func newStubSetStore(sets ...inventory.Set) *stubSetStore {
	s := &stubSetStore{sets: map[int]inventory.Set{}, nextID: 1}
	for _, set := range sets {
		s.sets[set.ID] = set
		if set.ID >= s.nextID {
			s.nextID = set.ID + 1
		}
	}
	return s
}

// Create stores a new set, assigning it the next free id.
func (s *stubSetStore) Create(_ context.Context, set inventory.Set) (inventory.Set, error) {
	s.gotSet = set
	if s.createErr != nil {
		return inventory.Set{}, s.createErr
	}
	set.ID = s.nextID
	s.nextID++
	s.sets[set.ID] = set
	return set, nil
}

// Get returns one stored set, or inventory.ErrSetNotFound.
//
// getErrAfter, when positive, makes every call past that many succeed-then-
// fail. Update reads twice (once to carry the organization forward, once to
// render what was stored), and the second read is the only way to reach the
// branch where a write lands but the response cannot be built.
func (s *stubSetStore) Get(_ context.Context, id int) (inventory.Set, error) {
	s.getCalls++
	if s.getErr != nil {
		return inventory.Set{}, s.getErr
	}
	if s.getErrAfter > 0 && s.getCalls > s.getErrAfter {
		return inventory.Set{}, errors.New("deliberate re-read failure")
	}
	set, ok := s.sets[id]
	if !ok {
		return inventory.Set{}, inventory.ErrSetNotFound
	}
	return set, nil
}

// List returns every stored set in ascending id order, honouring the
// query's Limit so the paging assertions have something to observe.
func (s *stubSetStore) List(_ context.Context, q inventory.SetQuery) ([]inventory.Set, error) {
	s.gotQuery = q
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]inventory.Set, 0, len(s.sets))
	for id := 1; id < s.nextID; id++ {
		if set, ok := s.sets[id]; ok && id > q.After {
			out = append(out, set)
		}
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// Update replaces a stored set.
func (s *stubSetStore) Update(_ context.Context, set inventory.Set) error {
	s.gotSet = set
	if s.updateErr != nil {
		return s.updateErr
	}
	s.sets[set.ID] = set
	return nil
}

// Delete removes a stored set.
func (s *stubSetStore) Delete(_ context.Context, id int) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.sets, id)
	return nil
}

// inventoryRouter mounts every inventory endpoint through the real
// api.NewRouter, with the same alwaysAuthenticated/fakeAdmitter shape the
// device and job handler tests use. Authentication and authorization have
// their own dedicated suites; this file's subject is the handler contract.
func inventoryRouter(t *testing.T, sets api.InventoryRepository) http.Handler {
	t.Helper()
	return inventoryRouterWithAuth(t, sets, alwaysAuthenticated)
}

// inventoryRouterWithAuth is inventoryRouter with the identity middleware
// left to the caller, so a test can mount a request pipeline that stamps no
// identity at all and reach Create's unauthorized branch.
func inventoryRouterWithAuth(t *testing.T, sets api.InventoryRepository, authMW func(http.Handler) http.Handler) http.Handler {
	t.Helper()
	handler := api.NewInventoryHandler(sets, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      authMW,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListInventories.Route(handler.List),
			apispec.GetInventory.Route(handler.Get),
			apispec.CreateInventory.Route(handler.Create),
			apispec.UpdateInventory.Route(handler.Update),
			apispec.DeleteInventory.Route(handler.Delete),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

// testSet builds a stored inventory with membership, so the detail and
// listing projections have something to differ about.
func testSet(id int, name string) inventory.Set {
	return inventory.Set{
		ID:             id,
		Name:           name,
		Description:    "a set of devices",
		OrganizationID: 7,
		Owner:          "owner@example.com",
		GroupIDs:       []int{11, 12},
		DeviceIDs:      []int{21},
	}
}

// decodedInventory is the shape these tests read back off the wire. It is
// deliberately a separate declaration from inventoryDTO: asserting against
// the handler's own struct would pass even if the JSON tags were wrong.
type decodedInventory struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Organization int    `json:"organization"`
	Owner        string `json:"owner"`
	GroupCount   int    `json:"group_count"`
	DeviceCount  int    `json:"device_count"`
	Groups       *[]int `json:"groups"`
	Devices      *[]int `json:"devices"`
	Links        []any  `json:"_links"`
}

func TestInventoryHandler_ListPagesAndCarriesTheQueryToTheStore(t *testing.T) {
	store := newStubSetStore(testSet(1, "production"), testSet(2, "staging"), testSet(3, "lab"))
	router := inventoryRouter(t, store)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventories?limit=2&after=1&q=prod", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Inventories []decodedInventory `json:"inventories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Inventories) != 2 {
		t.Fatalf("returned %d inventories, want 2", len(got.Inventories))
	}
	if store.gotQuery.Limit != 2 || store.gotQuery.After != 1 || store.gotQuery.Search != "prod" {
		t.Errorf("store saw %+v, want Limit 2, After 1, Search \"prod\"", store.gotQuery)
	}
}

// TestInventoryHandler_ListOmitsMembership proves the listing projection
// carries counts and not member ids. Inlining every device id would make
// opening a page of twenty inventories cost twenty fleet reads for data the
// page does not render, which is the reason toInventoryDTO takes a flag.
func TestInventoryHandler_ListOmitsMembership(t *testing.T) {
	router := inventoryRouter(t, newStubSetStore(testSet(1, "production")))

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventories", "")
	var got struct {
		Inventories []decodedInventory `json:"inventories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.Inventories[0].Groups != nil || got.Inventories[0].Devices != nil {
		t.Errorf("listing carried membership: groups=%v devices=%v", got.Inventories[0].Groups, got.Inventories[0].Devices)
	}
	if got.Inventories[0].GroupCount != 2 || got.Inventories[0].DeviceCount != 1 {
		t.Errorf("counts = %d groups, %d devices; want 2 and 1",
			got.Inventories[0].GroupCount, got.Inventories[0].DeviceCount)
	}
}

func TestInventoryHandler_ListRejectsMalformedPaging(t *testing.T) {
	router := inventoryRouter(t, newStubSetStore())

	for _, target := range []string{
		"/api/v1/inventories?after=-1",
		"/api/v1/inventories?after=nope",
		"/api/v1/inventories?limit=0",
		"/api/v1/inventories?limit=nope",
	} {
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400", target, rec.Code)
		}
	}
}

// TestInventoryHandler_ListStoreFailureIsAGeneric500 proves the store's own
// error text, which can name a table or a column, never reaches the caller.
func TestInventoryHandler_ListStoreFailureIsAGeneric500(t *testing.T) {
	store := newStubSetStore()
	store.listErr = errors.New("pq: relation \"inventories\" does not exist")
	router := inventoryRouter(t, store)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventories", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "relation") || strings.Contains(body, "inventories\\\"") {
		t.Errorf("store error text leaked into the response: %s", body)
	}
}

// TestInventoryHandler_GetCarriesMembership proves the detail projection
// does carry the ids the listing omits, and that an empty membership
// renders as [] rather than null so a client never has to guess which one
// the absence meant.
func TestInventoryHandler_GetCarriesMembership(t *testing.T) {
	empty := inventory.Set{ID: 2, Name: "empty", OrganizationID: 7}
	router := inventoryRouter(t, newStubSetStore(testSet(1, "production"), empty))

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventories/1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got decodedInventory
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.Groups == nil || len(*got.Groups) != 2 || got.Devices == nil || len(*got.Devices) != 1 {
		t.Fatalf("detail did not carry membership: %+v", got)
	}
	if got.Owner != "owner@example.com" {
		t.Errorf("owner = %q, want it carried through", got.Owner)
	}

	rec = doJSON(t, router, http.MethodGet, "/api/v1/inventories/2", "")
	var emptyGot decodedInventory
	if err := json.Unmarshal(rec.Body.Bytes(), &emptyGot); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if emptyGot.Groups == nil || emptyGot.Devices == nil {
		t.Errorf("empty membership rendered as null, want []: %s", rec.Body.String())
	}
}

func TestInventoryHandler_GetRejectsMalformedIDAndReports404(t *testing.T) {
	router := inventoryRouter(t, newStubSetStore())

	for _, tc := range []struct {
		target string
		want   int
	}{
		{"/api/v1/inventories/0", http.StatusBadRequest},
		{"/api/v1/inventories/-1", http.StatusBadRequest},
		{"/api/v1/inventories/abc", http.StatusBadRequest},
		{"/api/v1/inventories/99", http.StatusNotFound},
	} {
		rec := doJSON(t, router, http.MethodGet, tc.target, "")
		if rec.Code != tc.want {
			t.Errorf("GET %s: status = %d, want %d", tc.target, rec.Code, tc.want)
		}
	}
}

// TestInventoryHandler_CreateTakesTheOwnerFromTheIdentity is the important
// one: an owner a submitter could name is an audit trail a submitter could
// forge, so the recorded owner must come from the authenticated identity.
func TestInventoryHandler_CreateTakesTheOwnerFromTheIdentity(t *testing.T) {
	store := newStubSetStore()
	router := inventoryRouter(t, store)

	body := `{"name":"production","description":"prod hosts","organization":7,"groups":[1],"devices":[2]}`
	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventories", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if store.gotSet.Owner != "test-user" {
		t.Errorf("owner = %q, want the authenticated subject \"test-user\"", store.gotSet.Owner)
	}
	if store.gotSet.OrganizationID != 7 {
		t.Errorf("organization = %d, want 7", store.gotSet.OrganizationID)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/inventories/1" {
		t.Errorf("Location = %q, want /api/v1/inventories/1", loc)
	}
}

// TestInventoryHandler_CreateRefusesAnUndeclaredField proves the write DTO
// is closed rather than merely ignoring what it does not recognise.
//
// The distinction matters for exactly the field this test submits. Ignoring
// an "owner" key would let a caller believe they had set an owner that the
// server silently overwrote from the identity; refusing it says so. That is
// decodeJSON's DisallowUnknownFields, and it is the JSON-side twin of
// view.NewValues refusing an undeclared form key, so mass assignment is
// structurally impossible on both surfaces rather than remembered on each.
func TestInventoryHandler_CreateRefusesAnUndeclaredField(t *testing.T) {
	store := newStubSetStore()
	router := inventoryRouter(t, store)

	body := `{"name":"production","organization":7,"owner":"attacker@example.com"}`
	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventories", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if len(store.sets) != 0 {
		t.Error("a refused submission still reached the store")
	}
}

// TestInventoryHandler_CreateWithoutAnIdentityIs401 reaches the branch that
// only exists because a route could in principle be mounted outside the
// identity middleware. It fails closed rather than recording an empty owner.
func TestInventoryHandler_CreateWithoutAnIdentityIs401(t *testing.T) {
	anonymous := func(next http.Handler) http.Handler { return next }
	router := inventoryRouterWithAuth(t, newStubSetStore(), anonymous)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventories", `{"name":"x","organization":1}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestInventoryHandler_CreateRejectsMalformedJSON(t *testing.T) {
	router := inventoryRouter(t, newStubSetStore())

	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventories", `{"name":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// TestInventoryHandler_StoreErrorsMapToTheirStatus walks every classified
// branch of respondStoreError. The cross-tenant case is the one that
// matters: it must be a 403 rather than a 400, because the submission is
// well formed and the caller is authenticated, and the message must not
// name which devices belong to somebody else.
func TestInventoryHandler_StoreErrorsMapToTheirStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		want   int
		absent string
	}{
		{"cross tenant member", inventory.ErrCrossTenantMember, http.StatusForbidden, ""},
		{"duplicate name", inventory.ErrSetExists, http.StatusConflict, ""},
		{"not found", inventory.ErrSetNotFound, http.StatusNotFound, ""},
		{"unclassified", errors.New("pq: duplicate key value violates unique constraint \"inventories_name_key\""), http.StatusInternalServerError, "constraint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newStubSetStore()
			store.createErr = tc.err
			router := inventoryRouter(t, store)

			rec := doJSON(t, router, http.MethodPost, "/api/v1/inventories", `{"name":"x","organization":1}`)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.absent != "" && strings.Contains(rec.Body.String(), tc.absent) {
				t.Errorf("store error text leaked: %s", rec.Body.String())
			}
			if tc.err == inventory.ErrCrossTenantMember {
				// The refusal says what was refused without answering the
				// question the caller was probing with.
				if strings.Contains(rec.Body.String(), "device") {
					t.Errorf("cross-tenant refusal named devices: %s", rec.Body.String())
				}
			}
		})
	}
}

// TestInventoryHandler_UpdateKeepsTheStoredOrganizationAndOwner proves the
// two immutable facts survive an edit that tries to change them. Moving an
// inventory between tenants would silently re-scope every RoleBinding
// pointing at it, which is a migration rather than an edit.
func TestInventoryHandler_UpdateKeepsTheStoredOrganizationAndOwner(t *testing.T) {
	store := newStubSetStore(testSet(1, "production"))
	router := inventoryRouter(t, store)

	body := `{"name":"renamed","description":"new","organization":999,"groups":[5],"devices":[6]}`
	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if store.gotSet.OrganizationID != 7 {
		t.Errorf("organization = %d, want the stored 7 rather than the submitted 999", store.gotSet.OrganizationID)
	}
	if store.gotSet.Owner != "owner@example.com" {
		t.Errorf("owner = %q, want the stored value", store.gotSet.Owner)
	}
	if store.gotSet.Name != "renamed" {
		t.Errorf("name = %q, want the submitted \"renamed\"", store.gotSet.Name)
	}
	if len(store.gotSet.GroupIDs) != 1 || store.gotSet.GroupIDs[0] != 5 {
		t.Errorf("groups = %v, want the submitted [5] replacing the stored set", store.gotSet.GroupIDs)
	}
}

func TestInventoryHandler_UpdateErrorBranches(t *testing.T) {
	t.Run("malformed id", func(t *testing.T) {
		router := inventoryRouter(t, newStubSetStore())
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/abc", `{"name":"x"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		router := inventoryRouter(t, newStubSetStore(testSet(1, "production")))
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/1", `{"name":`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing record is a 404 not a store error", func(t *testing.T) {
		router := inventoryRouter(t, newStubSetStore())
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/9", `{"name":"x"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("write failure", func(t *testing.T) {
		store := newStubSetStore(testSet(1, "production"))
		store.updateErr = inventory.ErrCrossTenantMember
		router := inventoryRouter(t, store)
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/1", `{"name":"x","devices":[404]}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	// The write lands and the read back to render it fails. Reported as a
	// 500 rather than a 200 with stale content, because answering with the
	// pre-edit record would tell the caller their change did not apply when
	// it did.
	t.Run("re-read after a successful write", func(t *testing.T) {
		store := newStubSetStore(testSet(1, "production"))
		store.getErrAfter = 1
		router := inventoryRouter(t, store)
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/1", `{"name":"renamed"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
		}
		if store.sets[1].Name != "renamed" {
			t.Error("the write did not land, so this test is not exercising the branch it claims to")
		}
	})
}

// TestInventoryHandler_CreateFailsClosedWithoutAnIdentity calls the handler
// directly rather than through a router, and the reason is worth recording.
//
// api.RequireScope answers 401 on a missing identity before any handler
// runs, so mounted on its declared route this branch is unreachable. It
// exists as defence in depth for a route mounted outside that middleware,
// where the alternative is recording an empty string as the owner of a
// grant surface. A branch that can only be reached by calling the handler
// directly is still worth having and still worth proving.
func TestInventoryHandler_CreateFailsClosedWithoutAnIdentity(t *testing.T) {
	store := newStubSetStore()
	handler := api.NewInventoryHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/inventories", strings.NewReader(`{"name":"x","organization":1}`))
	req.Header.Set("Content-Type", "application/json")
	handler.Create(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if len(store.sets) != 0 {
		t.Error("an unauthenticated create still reached the store")
	}
}

func TestInventoryHandler_Delete(t *testing.T) {
	t.Run("removes the record", func(t *testing.T) {
		store := newStubSetStore(testSet(1, "production"))
		router := inventoryRouter(t, store)

		rec := doJSON(t, router, http.MethodDelete, "/api/v1/inventories/1", "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
		}
		if _, still := store.sets[1]; still {
			t.Error("inventory survived its own delete")
		}
	})

	t.Run("malformed id", func(t *testing.T) {
		router := inventoryRouter(t, newStubSetStore())
		rec := doJSON(t, router, http.MethodDelete, "/api/v1/inventories/abc", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing record", func(t *testing.T) {
		store := newStubSetStore()
		store.deleteErr = inventory.ErrSetNotFound
		router := inventoryRouter(t, store)
		rec := doJSON(t, router, http.MethodDelete, "/api/v1/inventories/9", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// TestNewInventoryHandler_NilLoggerFallsBack proves the constructor's own
// guard, so a caller that omits a logger gets the process default rather
// than a nil dereference on the first store failure.
func TestNewInventoryHandler_NilLoggerFallsBack(t *testing.T) {
	store := newStubSetStore()
	store.listErr = errors.New("boom")
	handler := api.NewInventoryHandler(store, nil)

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.ListInventories.Route(handler.List)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventories", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

// TestInventoryHandler_PatchOmittingMembershipLeavesItAlone is the same
// regression as the teams one, and it matters more here.
//
// An inventory is a grant surface: sharing one is a role binding at
// inventory scope, so its membership is a permission written in another
// vocabulary. Silently emptying it on a PATCH that only renamed the
// inventory would change what every share of it covers, with nothing in the
// request saying so.
func TestInventoryHandler_PatchOmittingMembershipLeavesItAlone(t *testing.T) {
	store := newStubSetStore(testSet(1, "production"))
	router := inventoryRouter(t, store)

	membership := func() (groups, devices []int) {
		t.Helper()
		rec := doJSON(t, router, http.MethodGet, "/api/v1/inventories/1", "")
		var got decodedInventory
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		if got.Groups == nil || got.Devices == nil {
			t.Fatal("membership rendered as an absent key")
		}
		return *got.Groups, *got.Devices
	}

	t.Run("omitting both keys preserves both", func(t *testing.T) {
		if rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/1", `{"name":"renamed"}`); rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		groups, devices := membership()
		if len(groups) != 2 || len(devices) != 1 {
			t.Errorf("membership became %v groups and %v devices after a rename that never mentioned it", groups, devices)
		}
	})

	// Each list is independent: naming one must not clear the other.
	t.Run("naming one list leaves the other alone", func(t *testing.T) {
		if rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/1", `{"name":"renamed","devices":[99]}`); rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		groups, devices := membership()
		if len(groups) != 2 {
			t.Errorf("groups = %v, want them untouched by a device-only edit", groups)
		}
		if len(devices) != 1 || devices[0] != 99 {
			t.Errorf("devices = %v, want the submitted [99]", devices)
		}
	})

	t.Run("an explicit empty list still clears", func(t *testing.T) {
		if rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventories/1", `{"name":"renamed","groups":[],"devices":[]}`); rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		groups, devices := membership()
		if len(groups) != 0 || len(devices) != 0 {
			t.Errorf("membership = %v / %v after explicit empty lists, want both cleared", groups, devices)
		}
	})
}
