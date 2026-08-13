package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// This file covers the list, create, and update handlers: the paging
// contract, the JSON body rules every later endpoint in this API inherits
// from decodeJSON, and the concurrency guard that makes update a real
// PATCH rather than a blind overwrite.

// testDevice builds a stored device through the same factory the
// repository hydrates rows with, so these tests exercise the real
// hydration path rather than a hand-made stand-in.
func testDevice(t *testing.T, id, name string) pkginventory.InventoryItem {
	t.Helper()
	item, err := inventory.NewItemFactory().Build(record.Record{
		ID:         pkginventory.DeviceID(id),
		Name:       name,
		Type:       "linux_server",
		Properties: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"},
		Tags:       []pkginventory.Tag{"core"},
		State:      pkginventory.StateActive,
		Source:     pkginventory.SourceAuthority{Plugin: "test"},
	})
	if err != nil {
		t.Fatalf("building test device %q: %v", name, err)
	}
	return item
}

func doJSON(t *testing.T, router http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestDeviceHandler_ListReturnsAPageAndACursor(t *testing.T) {
	repo := &stubDeviceRepo{items: []pkginventory.InventoryItem{
		testDevice(t, "id-1", "router-1"),
		testDevice(t, "id-2", "router-2"),
		testDevice(t, "id-3", "router-3"),
	}}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventory/devices?limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Devices    []struct{ Name string } `json:"devices"`
		NextCursor string                  `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("returned %d devices, want 2", len(got.Devices))
	}
	// The cursor is the last returned device's ID, so the next request
	// resumes precisely where this page stopped.
	if got.NextCursor != "id-2" {
		t.Errorf("next_cursor = %q, want id-2", got.NextCursor)
	}
}

// A final page must not advertise a next one. The handler over-fetches by
// one to observe this rather than inferring it from a full page, which
// would produce a dead cursor whenever the total is an exact multiple of
// the limit.
func TestDeviceHandler_ListOmitsCursorOnTheFinalPage(t *testing.T) {
	repo := &stubDeviceRepo{items: []pkginventory.InventoryItem{
		testDevice(t, "id-1", "router-1"),
		testDevice(t, "id-2", "router-2"),
	}}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventory/devices?limit=2", "")
	var got struct {
		Devices    []json.RawMessage `json:"devices"`
		NextCursor string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("returned %d devices, want 2", len(got.Devices))
	}
	if got.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty on the final page", got.NextCursor)
	}
}

func TestDeviceHandler_ListRejectsUnusableParameters(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{})

	for _, target := range []string{
		"/api/v1/inventory/devices?limit=0",
		"/api/v1/inventory/devices?limit=-3",
		"/api/v1/inventory/devices?limit=many",
	} {
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", target, rec.Code)
		}
	}
}

// The cap is the server's, not the caller's: an unbounded limit is a
// request to hold the whole fleet in memory at once.
func TestDeviceHandler_ListCapsAnOversizedLimit(t *testing.T) {
	items := make([]pkginventory.InventoryItem, 0, 250)
	for i := range 250 {
		items = append(items, testDevice(t, string(rune('a'+i%26))+string(rune('a'+i/26)), string(rune('a'+i%26))+string(rune('a'+i/26))))
	}
	repo := &stubDeviceRepo{items: items}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventory/devices?limit=100000", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got struct {
		Devices []json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Devices) > 200 {
		t.Errorf("returned %d devices, want the 200 cap enforced", len(got.Devices))
	}
}

func TestDeviceHandler_CreateStoresAndReadsBack(t *testing.T) {
	stored := testDevice(t, "id-new", "router-new")
	repo := &stubDeviceRepo{item: stored}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventory/devices",
		`{"name":"router-new","type":"linux_server","tags":["core"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if repo.created == nil {
		t.Fatal("Create never reached the repository")
	}
	if repo.created.Name() != "router-new" {
		t.Errorf("created name = %q, want router-new", repo.created.Name())
	}
	// The caller does not supply an identifier; the handler mints one, or
	// Repository.Create would write an empty device_id the schema refuses.
	if repo.created.ID() == "" {
		t.Error("created device carries no ID")
	}
}

func TestDeviceHandler_CreateRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		wantStatus int
		why        string
	}{
		{"missing name", `{"type":"linux_server"}`, http.StatusBadRequest,
			"a device with no name cannot be addressed afterwards"},
		{"missing type", `{"name":"router-1"}`, http.StatusBadRequest,
			"the type is what decides which Go type and capabilities the device gets"},
		{"unknown type", `{"name":"router-1","type":"toaster"}`, http.StatusUnprocessableEntity,
			"the body parsed; the value just names nothing this build registers"},
		{"unknown state", `{"name":"router-1","type":"linux_server","state":"vibing"}`, http.StatusUnprocessableEntity,
			"an unrecognised lifecycle state must never be silently promoted to active"},
		{"control character in name", "{\"name\":\"rou\\u0000ter\",\"type\":\"linux_server\"}", http.StatusBadRequest,
			"a NUL reaches a storage query intact"},
		{"malformed json", `{"name":`, http.StatusBadRequest,
			"the decoder's own message can quote the caller's bytes back at them"},
		{"unknown field", `{"name":"r","type":"linux_server","is_admin":true}`, http.StatusBadRequest,
			"silently dropping a field a client believed it sent is how a caller ends up sure it set something it did not"},
		{"trailing garbage", `{"name":"r","type":"linux_server"} {"name":"s"}`, http.StatusBadRequest,
			"a second object after a valid one is data nobody reads"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := deviceRouter(t, &stubDeviceRepo{})
			rec := doJSON(t, router, http.MethodPost, "/api/v1/inventory/devices", tc.body)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, tc.why)
			}
		})
	}
}

func TestDeviceHandler_CreateRejectsNonJSONContentType(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/inventory/devices",
		strings.NewReader(`{"name":"r","type":"linux_server"}`))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", rec.Code)
	}
}

func TestDeviceHandler_CreateRejectsAnOversizedBody(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{})
	huge := `{"name":"` + strings.Repeat("a", 128<<10) + `","type":"linux_server"}`

	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventory/devices", huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

// A duplicate name is a conflict, never a silent overwrite: the whole
// reason Create is separate from Save is that a caller must be able to
// tell a first-time onboard from a re-sync.
func TestDeviceHandler_CreateReportsADuplicateAsConflict(t *testing.T) {
	repo := &stubDeviceRepo{createErr: inventory.ErrItemExists}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventory/devices",
		`{"name":"router-1","type":"linux_server"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestDeviceHandler_UpdateAppliesTagsAndState(t *testing.T) {
	repo := &stubDeviceRepo{item: testDevice(t, "id-1", "router-1")}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1",
		`{"tags":["edge","dmz"],"state":"quarantined"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if repo.saved == nil {
		t.Fatal("Save never reached the repository")
	}
	if got := repo.saved.State(); got != pkginventory.StateQuarantined {
		t.Errorf("saved state = %v, want quarantined", got)
	}
	if got := repo.saved.Tags(); len(got) != 2 || got[0] != "edge" {
		t.Errorf("saved tags = %v, want [edge dmz]", got)
	}
	// The rebuild carries identity and the concurrency token forward; a
	// write that reset either would destroy the audit trail the schema
	// exists to protect.
	if repo.saved.ID() != "id-1" {
		t.Errorf("saved ID = %q, want id-1 carried forward", repo.saved.ID())
	}
}

// Omitting a field leaves it alone. That is what makes this a PATCH: a
// client editing only the state must not silently clear the tags.
func TestDeviceHandler_UpdateLeavesOmittedFieldsAlone(t *testing.T) {
	repo := &stubDeviceRepo{item: testDevice(t, "id-1", "router-1")}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1", `{"state":"quarantined"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := repo.saved.Tags(); len(got) != 1 || got[0] != "core" {
		t.Errorf("saved tags = %v, want the stored [core] left untouched", got)
	}
}

// An empty array is a real instruction, distinguishable from an absent
// field only because the wire type is a pointer.
func TestDeviceHandler_UpdateClearsTagsOnAnEmptyArray(t *testing.T) {
	repo := &stubDeviceRepo{item: testDevice(t, "id-1", "router-1")}
	router := deviceRouter(t, repo)

	if rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1", `{"tags":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := repo.saved.Tags(); len(got) != 0 {
		t.Errorf("saved tags = %v, want them cleared", got)
	}
}

// The concurrency guard is the reason this is not a blind PUT: a second
// writer's change must be refused rather than lost.
func TestDeviceHandler_UpdateReportsAConcurrentWriteAsConflict(t *testing.T) {
	repo := &stubDeviceRepo{
		item:    testDevice(t, "id-1", "router-1"),
		saveErr: inventory.ErrVersionConflict,
	}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1", `{"state":"quarantined"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "reload and retry") {
		t.Errorf("body = %s, want it to tell the caller what to do", rec.Body.String())
	}
}

func TestDeviceHandler_UpdateRejectsAnUnknownState(t *testing.T) {
	repo := &stubDeviceRepo{item: testDevice(t, "id-1", "router-1")}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1", `{"state":"vibing"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if repo.saved != nil {
		t.Error("an invalid state reached the repository")
	}
}
