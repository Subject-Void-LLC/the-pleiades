package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// stubRunbookRepo answers from fixed values, so each branch of the catalog
// handlers is reachable without a real directory behind it. RunbookRepository
// is the narrow read-only interface this package defines precisely so a
// test can supply a small double, per AGENTS.md's "mock interfaces, not
// concrete types".
type stubRunbookRepo struct {
	ids     []string
	rb      *runbook.Runbook
	listErr error
	getErr  error
}

func (s stubRunbookRepo) List(context.Context) ([]string, error) { return s.ids, s.listErr }

func (s stubRunbookRepo) Get(context.Context, string) (*runbook.Runbook, error) {
	return s.rb, s.getErr
}

func runbooksRouter(t *testing.T, repo api.RunbookRepository) http.Handler {
	t.Helper()
	handler := api.NewRunbookHandler(repo, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/runbooks", Scope: auth.ScopeRunbookRead, Rel: auth.RelCollection, Handler: handler.List},
			{Method: http.MethodGet, Pattern: "/runbooks/{id}", Scope: auth.ScopeRunbookRead, Rel: auth.RelSelf, Handler: handler.Get},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestRunbookHandler_ListReturnsTheCatalog(t *testing.T) {
	router := runbooksRouter(t, stubRunbookRepo{ids: []string{"alpha", "bravo"}})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/runbooks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Runbooks []string `json:"runbooks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Runbooks) != 2 || got.Runbooks[0] != "alpha" {
		t.Errorf("runbooks = %v, want [alpha bravo]", got.Runbooks)
	}
}

// An empty catalog carries [] rather than null: a client should not have
// to guess whether null meant "none" or "unknown".
func TestRunbookHandler_ListEmptyCatalogIsAnArray(t *testing.T) {
	router := runbooksRouter(t, stubRunbookRepo{ids: nil})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/runbooks", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !jsonContains(rec.Body.String(), `"runbooks":[]`) {
		t.Errorf("body = %s, want an empty array rather than null", rec.Body.String())
	}
}

func TestRunbookHandler_ListReportsASourceFailure(t *testing.T) {
	router := runbooksRouter(t, stubRunbookRepo{listErr: errors.New("/srv/runbooks: permission denied")})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/runbooks", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	// The source's error can name filesystem paths, which a caller
	// holding only runbook:read has no business reading.
	if jsonContains(rec.Body.String(), "/srv/runbooks") {
		t.Errorf("body = %s, want the path withheld", rec.Body.String())
	}
}

func TestRunbookHandler_GetReturnsCompiledRequirements(t *testing.T) {
	router := runbooksRouter(t, stubRunbookRepo{rb: &runbook.Runbook{
		ID:            "alpha",
		Required:      []capability.Name{"SSHCapable", "AptCapable"},
		Interruptible: true,
	}})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/runbooks/alpha", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		ID            string   `json:"id"`
		Required      []string `json:"required_capabilities"`
		Interruptible bool     `json:"interruptible"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if got.ID != "alpha" {
		t.Errorf("id = %q, want alpha", got.ID)
	}
	if len(got.Required) != 2 || got.Required[0] != "SSHCapable" {
		t.Errorf("required_capabilities = %v, want [SSHCapable AptCapable]", got.Required)
	}
	if !got.Interruptible {
		t.Error("interruptible = false, want it carried through")
	}
}

func TestRunbookHandler_GetReportsAMissingRunbook(t *testing.T) {
	router := runbooksRouter(t, stubRunbookRepo{getErr: runbook.ErrNotFound})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/runbooks/ghost", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// A malformed id, a traversal attempt, or a file that will not compile all
// arrive as a bad request, and the rejected value is never echoed back.
func TestRunbookHandler_GetRejectsAnUnusableIDWithoutEchoingIt(t *testing.T) {
	router := runbooksRouter(t, stubRunbookRepo{getErr: errors.New("invalid runbook id: must match ^[a-z]+$")})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/runbooks/..%2f..%2fetc%2fpasswd", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if jsonContains(rec.Body.String(), "passwd") {
		t.Errorf("body = %s, want the rejected id withheld", rec.Body.String())
	}
}
