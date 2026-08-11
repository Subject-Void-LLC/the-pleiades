package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// This file covers the branches a working repository never produces: the
// backend failing partway through, the read-only wrapper refusing a write,
// and the parameter guards that stop a caller-controlled value reaching a
// storage query. They are separated from devices_write_test.go so the
// happy-path file stays about the contract rather than the failure matrix.

func TestDeviceHandler_ListReportsARepositoryFailure(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{listErr: errors.New("backend down")})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/inventory/devices", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// The driver's own message can name tables, columns, and hosts, so
	// only the category reaches the caller.
	if body := rec.Body.String(); strings.Contains(body, "backend down") {
		t.Errorf("body = %s, want the underlying error withheld", body)
	}
}

func TestDeviceHandler_ListRefusesControlCharactersInParameters(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{})

	// Both parameters reach a storage query, so both get the guard the
	// {name} path parameter already has.
	for _, target := range []string{
		"/api/v1/inventory/devices?group=core%00admin",
		"/api/v1/inventory/devices?after=id%0d%0ainjected",
	} {
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", target, rec.Code)
		}
	}
}

func TestDeviceHandler_ListPassesGroupAndCursorThrough(t *testing.T) {
	repo := &recordingSelectorRepo{}
	router := deviceRouter(t, repo)

	if rec := doJSON(t, router, http.MethodGet, "/api/v1/inventory/devices?group=core&after=id-7&limit=5", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if repo.sel.GroupName != "core" {
		t.Errorf("GroupName = %q, want core", repo.sel.GroupName)
	}
	if repo.sel.After != "id-7" {
		t.Errorf("After = %q, want id-7", repo.sel.After)
	}
	// One more than asked for, so a next page is observed rather than
	// guessed at from a full page.
	if repo.sel.Limit != 6 {
		t.Errorf("Limit = %d, want 6 (the page size plus the lookahead)", repo.sel.Limit)
	}
}

// recordingSelectorRepo captures the Selector the handler built, which is
// the only way to prove the query parameters reached the port rather than
// being parsed and dropped.
type recordingSelectorRepo struct {
	stubDeviceRepo
	sel pkginventory.Selector
}

func (r *recordingSelectorRepo) GetGroup(ctx context.Context, sel pkginventory.Selector) (inventory.Iterator, error) {
	r.sel = sel
	return r.stubDeviceRepo.GetGroup(ctx, sel)
}

func TestDeviceHandler_CreateReportsAReadOnlyInventory(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{createErr: inventory.ErrInventoryReadOnly})

	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventory/devices",
		`{"name":"router-1","type":"linux_server"}`)
	// Not 403: RequireScope owns authorization failures, and conflating
	// the two would make a deliberately non-mutating run look like a
	// permissions problem to whoever is debugging it.
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

// The device is stored, then read back so the response carries the ID and
// version the repository assigned. A failure on that second read is still
// a failed request, not a silent 201 describing state nobody confirmed.
func TestDeviceHandler_CreateReportsAFailedReadBack(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{getErr: errors.New("gone")})

	rec := doJSON(t, router, http.MethodPost, "/api/v1/inventory/devices",
		`{"name":"router-1","type":"linux_server"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestDeviceHandler_CreateAcceptsAnExplicitState(t *testing.T) {
	stored := testDevice(t, "id-1", "router-1")
	repo := &stubDeviceRepo{item: stored}
	router := deviceRouter(t, repo)

	if rec := doJSON(t, router, http.MethodPost, "/api/v1/inventory/devices",
		`{"name":"router-1","type":"linux_server","state":"quarantined"}`); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if got := repo.created.State(); got != pkginventory.StateQuarantined {
		t.Errorf("created state = %v, want quarantined", got)
	}
}

func TestDeviceHandler_UpdateRejectsAnUnusableName(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{})

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/%00", `{"tags":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDeviceHandler_UpdateRejectsAMalformedBody(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{item: testDevice(t, "id-1", "router-1")})

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1", `{"tags":`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDeviceHandler_UpdateReportsAMissingDevice(t *testing.T) {
	router := deviceRouter(t, &stubDeviceRepo{getErr: inventory.ErrItemNotFound})

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/ghost", `{"tags":[]}`)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestDeviceHandler_UpdateReportsAFailedReadBack(t *testing.T) {
	// The first read succeeds and the save succeeds; the read-back is what
	// fails, which must not be reported as a successful update.
	repo := &readBackFailureRepo{item: testDevice(t, "id-1", "router-1")}
	router := deviceRouter(t, repo)

	rec := doJSON(t, router, http.MethodPatch, "/api/v1/inventory/devices/router-1", `{"tags":["x"]}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// readBackFailureRepo answers the first GetByName and fails every later
// one, which is the only way to reach the read-back branch specifically.
type readBackFailureRepo struct {
	stubDeviceRepo
	item  pkginventory.InventoryItem
	reads int
}

func (r *readBackFailureRepo) GetByName(_ context.Context, _ string) (pkginventory.InventoryItem, error) {
	r.reads++
	if r.reads == 1 {
		return r.item, nil
	}
	return nil, errors.New("gone")
}
