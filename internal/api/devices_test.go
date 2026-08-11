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
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// This file covers the device handlers' own contract: how a repository
// failure becomes a status code, and what never reaches the caller. The
// Release Gate (hateoas_release_test.go) exercises the happy paths against
// a real repository; these are the branches a real repository does not
// produce on demand.

// stubDeviceRepo returns whatever it was configured to return, so each
// error branch can be reached deterministically.
type stubDeviceRepo struct {
	item      pkginventory.InventoryItem
	items     []pkginventory.InventoryItem
	getErr    error
	listErr   error
	createErr error
	saveErr   error
	retireErr error

	retiredName string
	created     pkginventory.InventoryItem
	saved       pkginventory.InventoryItem
}

func (s *stubDeviceRepo) GetGroup(_ context.Context, sel pkginventory.Selector) (inventory.Iterator, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	items := s.items
	if sel.Limit > 0 && len(items) > sel.Limit {
		items = items[:sel.Limit]
	}
	return &stubIterator{items: items}, nil
}

func (s *stubDeviceRepo) GetByName(_ context.Context, _ string) (pkginventory.InventoryItem, error) {
	return s.item, s.getErr
}

func (s *stubDeviceRepo) Create(_ context.Context, item pkginventory.InventoryItem) error {
	s.created = item
	return s.createErr
}

func (s *stubDeviceRepo) Save(_ context.Context, item pkginventory.InventoryItem) error {
	s.saved = item
	return s.saveErr
}

func (s *stubDeviceRepo) Retire(_ context.Context, name string) error {
	s.retiredName = name
	return s.retireErr
}

// stubIterator walks a fixed slice, matching the real iterators' contract:
// Next reports whether an item is available, and a cancelled context stops
// the walk rather than yielding items the caller no longer wants.
type stubIterator struct {
	items []pkginventory.InventoryItem
	index int
	cur   pkginventory.InventoryItem
}

func (i *stubIterator) Next(ctx context.Context) bool {
	if ctx.Err() != nil || i.index >= len(i.items) {
		return false
	}
	i.cur = i.items[i.index]
	i.index++
	return true
}

func (i *stubIterator) Item() pkginventory.InventoryItem { return i.cur }
func (i *stubIterator) Error() error                     { return nil }
func (i *stubIterator) Close() error                     { return nil }

// deviceRouter mounts the device handlers over repo.
func deviceRouter(t *testing.T, repo api.DeviceRepository) http.Handler {
	t.Helper()
	handler := api.NewDeviceHandler(repo, inventory.NewItemFactory(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/inventory/devices", Scope: auth.ScopeInventoryRead, Rel: auth.RelCollection, Handler: handler.List},
			{Method: http.MethodPost, Pattern: "/inventory/devices", Scope: auth.ScopeInventoryWrite, Rel: auth.RelCreate, Handler: handler.Create},
			{Method: http.MethodGet, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryRead, Rel: auth.RelSelf, Handler: handler.Get},
			{Method: http.MethodPatch, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelUpdate, Handler: handler.Update},
			{Method: http.MethodDelete, Pattern: "/inventory/devices/{name}", Scope: auth.ScopeInventoryWrite, Rel: auth.RelDelete, Handler: handler.Delete},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestDeviceHandler_RepositoryErrorMapping(t *testing.T) {
	// The sentinels are what make these distinguishable at all. Without
	// them a missing device and a broken database would both have to be
	// 500, which is why the Retire conformance suite asserts both adapters
	// return them.
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
		why        string
	}{
		{
			name:       "not found",
			err:        inventory.ErrItemNotFound,
			wantStatus: http.StatusNotFound,
			wantBody:   "device not found",
			why:        "a caller must be able to tell an absent device from a failing backend",
		},
		{
			name:       "read only",
			err:        inventory.ErrInventoryReadOnly,
			wantStatus: http.StatusConflict,
			wantBody:   "inventory is read-only",
			why:        "not 403: RequireScope owns authorization, and conflating the two makes a dry run look like a permissions problem",
		},
		{
			name:       "version conflict",
			err:        inventory.ErrVersionConflict,
			wantStatus: http.StatusConflict,
			wantBody:   "device was modified concurrently, reload and retry",
			why:        "the caller's next step is to reload, which the message has to say",
		},
		{
			name:       "unknown backend failure",
			err:        errors.New("pq: relation \"devices\" does not exist at 10.0.0.9:5432"),
			wantStatus: http.StatusInternalServerError,
			wantBody:   "internal error",
			why:        "the driver's own text names tables and hosts and must never reach an API caller",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := deviceRouter(t, &stubDeviceRepo{getErr: tc.err})

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/edge-01", nil))

			if rr.Code != tc.wantStatus {
				t.Errorf("status is %d, want %d (%s)", rr.Code, tc.wantStatus, tc.why)
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %q", rr.Body.String())
			}
			if body.Error != tc.wantBody {
				t.Errorf("message is %q, want %q", body.Error, tc.wantBody)
			}
		})
	}
}

func TestDeviceHandler_NeverLeaksTheDriverError(t *testing.T) {
	// Explicit, because the generic-message assertion above would still
	// pass if the driver text were appended somewhere else in the body.
	driverText := "pq: password authentication failed for user \"pleiades\""
	router := deviceRouter(t, &stubDeviceRepo{getErr: errors.New(driverText)})

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/edge-01", nil))

	for _, fragment := range []string{"pq:", "password", "pleiades"} {
		if strings.Contains(rr.Body.String(), fragment) {
			t.Errorf("response body leaks driver detail %q: %s", fragment, rr.Body.String())
		}
	}
}

func TestDeviceHandler_RejectsUnusableNamesWithoutEchoingThem(t *testing.T) {
	// The bound is deliberately a bound and not a character allowlist:
	// Phase 39's SQL audit round-tripped a device literally named
	// "'; DROP TABLE devices; --", so odd characters are supported. What
	// must never happen is the rejected value coming back in the body,
	// which is the reflected shape FAILURE_PATTERNS.md #72 names.
	//
	// The cases below are the ones that actually reach a handler, which
	// is narrower than it looks and was measured rather than assumed.
	// chi hands a handler the percent-encoded segment for "%0a", "%0d",
	// and "%2f" (they stay as the literal text "%0a" and cannot introduce
	// a newline or a path separator), but it decodes "%00" into a real NUL
	// byte. So NUL is the one control character that genuinely arrives,
	// and it is the one this guard has to catch. See
	// TestDeviceHandler_PercentEncodedControlsArriveEncoded below for the
	// other half of that finding.
	for _, tc := range []struct {
		name  string
		param string
	}{
		{"too long", strings.Repeat("a", 254)},
		{"null byte", "edge%00-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubDeviceRepo{getErr: errors.New("must not be reached")}
			router := deviceRouter(t, repo)

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/"+tc.param, nil))

			if rr.Code != http.StatusBadRequest {
				t.Errorf("status is %d, want 400", rr.Code)
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %q", rr.Body.String())
			}
			if body.Error != "invalid device name" {
				t.Errorf("message is %q, want the fixed rejection message", body.Error)
			}
		})
	}
}

func TestDeviceHandler_DeleteReturnsNoContentAndNamesTheDevice(t *testing.T) {
	repo := &stubDeviceRepo{}
	router := deviceRouter(t, repo)

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, api.APIVersionPrefix+"/inventory/devices/edge-01", nil))

	if rr.Code != http.StatusNoContent {
		t.Errorf("status is %d, want 204", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Errorf("204 carried a %d byte body", rr.Body.Len())
	}
	if repo.retiredName != "edge-01" {
		t.Errorf("repository was asked to retire %q, want %q", repo.retiredName, "edge-01")
	}
}

func TestDeviceHandler_DeleteMapsRepositoryErrors(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{retireErr: inventory.ErrItemNotFound})

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, api.APIVersionPrefix+"/inventory/devices/gone", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status is %d, want 404", rr.Code)
	}
}

// TestDeviceHandler_PercentEncodedControlsArriveEncoded pins down behavior
// this package depends on but does not implement, so a future chi or
// net/url upgrade that changes it fails here rather than silently widening
// what reaches a handler.
//
// Measured, not assumed: chi decodes "%00" into a literal NUL byte, and
// leaves "%0a", "%0d", and "%2f" as the three-character text they were
// written as. That asymmetry is why the name guard checks for a real NUL
// rather than for the string "%00", and why a percent-encoded newline is
// not a rejection case: no newline exists to reject. A device named
// "edge%0a-01" is an ordinary, if strange, name and is looked up as one.
func TestDeviceHandler_PercentEncodedControlsArriveEncoded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		param string
		want  string
	}{
		{"encoded newline", "edge%0a-01", "edge%0a-01"},
		{"encoded carriage return", "edge%0d-01", "edge%0d-01"},
		{"encoded slash", "edge%2f-01", "edge%2f-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &stubDeviceRepo{getErr: inventory.ErrItemNotFound}
			router := deviceRouter(t, repo)

			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/inventory/devices/"+tc.param, nil))

			// Treated as an ordinary name: looked up, and absent.
			if rr.Code != http.StatusNotFound {
				t.Errorf("status is %d, want 404: the encoded value is a legal name, not a rejection case", rr.Code)
			}
			// And whatever it was, it is not echoed back.
			if strings.Contains(rr.Body.String(), tc.param) {
				t.Errorf("response echoes the requested name: %s", rr.Body.String())
			}
			// The property that matters: no raw control character ever
			// reached the handler's own view of the name.
			if strings.ContainsAny(tc.want, "\r\n\x00") {
				t.Fatalf("expectation %q itself contains a raw control character", tc.want)
			}
		})
	}
}
