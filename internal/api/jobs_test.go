package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/google/uuid"
)

// jobsRouter mounts JobHandler.Get over a real dispatch.JobStore, the
// identical alwaysAuthenticated/fakeAdmitter/allowAllGenerator shape
// devices_test.go's own deviceRouter uses: this file's subject is
// JobHandler's own contract (UUID validation, error mapping, task
// rendering), not authentication or authorization, which
// hateoas_release_test.go and middleware_test.go already cover against
// the real thing.
func jobsRouter(t *testing.T, jobs api.JobRepository) http.Handler {
	t.Helper()
	handler := api.NewJobHandler(jobs)
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			{Method: http.MethodGet, Pattern: "/jobs", Scope: auth.ScopeJobRead, Rel: auth.RelCollection, Handler: handler.List},
			{Method: http.MethodGet, Pattern: "/jobs/{id}", Scope: auth.ScopeJobRead, Rel: auth.RelSelf, Handler: handler.Get},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

// erroringJobRepository is a JobRepository stub whose Get always returns a
// non-ErrJobNotFound error, so TestJobHandler_StoreErrorReturns500 can
// prove the "err, not ErrJobNotFound" branch of Get without a real backing
// store failure to trigger it. JobRepository is the narrow, read-only
// interface this package already defines specifically so a test can supply
// a small double instead of a full dispatch.JobStore (its own doc comment),
// matching AGENTS.md's "mock interfaces, not concrete types" allowance.
type erroringJobRepository struct{}

func (erroringJobRepository) Get(ctx context.Context, jobID string) (*dispatch.Job, []dispatch.JobTask, error) {
	return nil, nil, errors.New("deliberate store failure")
}

func (erroringJobRepository) List(ctx context.Context, after string, limit int) ([]*dispatch.Job, error) {
	return nil, errors.New("deliberate store failure")
}

// TestJobHandler_StoreErrorReturns500 proves a backing-store failure that
// is not dispatch.ErrJobNotFound is mapped to a generic 500, and that the
// store's own error text (which can name tables and columns) never reaches
// the response body, the identical posture devices.go's writeRepositoryError
// already takes for inventory.Repository failures.
func TestJobHandler_StoreErrorReturns500(t *testing.T) {
	router := jobsRouter(t, erroringJobRepository{})

	target := api.APIVersionPrefix + "/jobs/" + uuid.New().String()
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response %q: %v", rr.Body.String(), err)
	}
	if body.Error != "internal error" {
		t.Errorf("error message = %q, want the generic %q (the store's own error text must never reach the client)", body.Error, "internal error")
	}
}

func TestJobHandler_UnknownUUIDReturns404(t *testing.T) {
	router := jobsRouter(t, newTestJobStore(t))

	target := api.APIVersionPrefix + "/jobs/" + uuid.New().String()
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %q: %v", rr.Body.String(), err)
	}
	if body.Error != "job not found" {
		t.Errorf("error message = %q, want %q", body.Error, "job not found")
	}
}

// TestJobHandler_EmptyIDReturns400 mirrors logs_test.go's own
// TestStreamLogs_RequiresJobID: a trailing-slash-free path with an empty
// {id} segment does not route at all in chi (there is no segment for
// "/jobs/{id}" to bind), so this calls JobHandler.Get directly with a bare
// request chi never populated a URL param on, the same shape as any caller
// that reached this handler without going through the router, rather than
// through the router (which would answer chi's own generic 404, not this
// handler's own validation response, and would prove nothing about it).
func TestJobHandler_EmptyIDReturns400(t *testing.T) {
	handler := api.NewJobHandler(newTestJobStore(t))

	req := httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/jobs//", nil)
	rr := httptest.NewRecorder()
	handler.Get(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d: body %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

// TestJobHandler_MalformedIDReturns400 mirrors logs_test.go's own
// TestStreamLogs_RejectsSubjectInjectingJobIDs: both handlers take a
// caller-controlled {id} path segment and both refuse anything that is
// not a well-formed UUID before using it for anything, so both are tested
// the same way, against a battery of the same class of hostile and
// merely-malformed values, asserting the exact fixed rejection body and
// that the rejected value is never echoed back.
func TestJobHandler_MalformedIDReturns400(t *testing.T) {
	router := jobsRouter(t, newTestJobStore(t))

	for _, tc := range []struct {
		name string
		id   string
	}{
		{"not a uuid", "123"},
		{"wildcard", "*"},
		{"trailing garbage", "1f8c8a48-0f21-4c65-9a45-2f6bd9b0a111-extra"},
		{"newline injection", "1f8c8a48-0f21-4c65-9a45-2f6bd9b0a111\ndata: forged"},
		{"path traversal", "../../etc/passwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := api.APIVersionPrefix + "/jobs/" + url.PathEscape(tc.id)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))

			if rr.Code != http.StatusBadRequest {
				t.Errorf("id %q: status = %d, want %d: body %s", tc.id, rr.Code, http.StatusBadRequest, rr.Body.String())
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("id %q: body is not JSON: %q: %v", tc.id, rr.Body.String(), err)
			}
			if body.Error != "job id must be a UUID" {
				t.Errorf("id %q: error message = %q, want the fixed rejection message", tc.id, body.Error)
			}
		})
	}
}

// TestJobHandler_RendersJobWithTasks proves a job carrying both a
// dispatched and a skipped task renders both correctly: the skipped task's
// Reason is present and names the reason, and the dispatched task's Reason
// is genuinely absent from the wire body (LinkSet's own "absent, not
// empty-string" distinction, jobTaskDTO's own omitempty), not merely
// rendered as an empty string a client would have to special-case.
func TestJobHandler_RendersJobWithTasks(t *testing.T) {
	store := newTestJobStore(t)
	ctx := context.Background()

	job := &dispatch.Job{
		RunbookID: "pb-1", Actor: "operator@example.com",
		TemplateID: 12, TemplateName: "patch the edge routers",
		InventoryID: 7, OrganizationID: 3, Kind: "runbook",
	}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// BeginFanOut claims the fan-out and obtains the fencing token
	// RecordTask and Complete now require (internal/dispatch's
	// fencing-token fix), the same production sequence
	// Worker.HandleJobRequested itself follows.
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
		DeviceID:   "dev-1",
		DeviceName: "router-1",
		Outcome:    dispatch.OutcomeDispatched,
	}); err != nil {
		t.Fatalf("RecordTask (dispatched): %v", err)
	}
	if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
		DeviceID:   "dev-2",
		DeviceName: "router-2",
		Outcome:    dispatch.OutcomeSkipped,
		Reason:     `device "router-2" is quarantined, not active`,
	}); err != nil {
		t.Fatalf("RecordTask (skipped): %v", err)
	}
	if err := store.Complete(ctx, job.JobID, fence, 1, 1, 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	router := jobsRouter(t, store)
	target := api.APIVersionPrefix + "/jobs/" + job.JobID
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: body %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var body struct {
		JobID        string `json:"job_id"`
		RunbookID    string `json:"runbook_id"`
		State        string `json:"state"`
		Template     int    `json:"template"`
		TemplateName string `json:"template_name"`
		Inventory    int    `json:"inventory"`
		Organization int    `json:"organization"`
		Kind         string `json:"kind"`

		Dispatched int                          `json:"dispatched"`
		Skipped    int                          `json:"skipped"`
		Failed     int                          `json:"failed"`
		Tasks      []map[string]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %q: %v", rr.Body.String(), err)
	}

	if body.JobID != job.JobID || body.RunbookID != "pb-1" {
		t.Errorf("job fields = %+v, want JobID=%q RunbookID=pb-1", body, job.JobID)
	}
	// What a job says it came from, which is what replaced the group name
	// it used to carry: a template, the inventory it targeted, and the
	// tenant that inventory gave it.
	if body.Template != 12 || body.TemplateName != "patch the edge routers" {
		t.Errorf("job reports template %d/%q, want the one it was launched from", body.Template, body.TemplateName)
	}
	if body.Inventory != 7 || body.Organization != 3 {
		t.Errorf("job reports inventory %d in organization %d, want 7 in 3", body.Inventory, body.Organization)
	}
	if body.Kind != "runbook" {
		t.Errorf("job reports kind %q, want runbook", body.Kind)
	}
	if body.State != "completed" {
		t.Errorf("state = %q, want %q", body.State, "completed")
	}
	if body.Dispatched != 1 || body.Skipped != 1 || body.Failed != 0 {
		t.Errorf("tallies = dispatched=%d skipped=%d failed=%d, want 1/1/0", body.Dispatched, body.Skipped, body.Failed)
	}
	if len(body.Tasks) != 2 {
		t.Fatalf("tasks = %d entries, want 2", len(body.Tasks))
	}

	for _, task := range body.Tasks {
		var deviceID string
		if err := json.Unmarshal(task["device_id"], &deviceID); err != nil {
			t.Fatalf("task device_id is not a string: %v", err)
		}
		var outcome string
		if err := json.Unmarshal(task["outcome"], &outcome); err != nil {
			t.Fatalf("task outcome is not a string: %v", err)
		}
		reasonRaw, hasReason := task["reason"]

		switch deviceID {
		case "dev-1":
			if outcome != "dispatched" {
				t.Errorf("dev-1 outcome = %q, want %q", outcome, "dispatched")
			}
			if hasReason {
				t.Errorf("dev-1 (dispatched) carries a reason field at all (%s), want it entirely absent from the wire body", reasonRaw)
			}
		case "dev-2":
			if outcome != "skipped" {
				t.Errorf("dev-2 outcome = %q, want %q", outcome, "skipped")
			}
			if !hasReason {
				t.Fatal("dev-2 (skipped) carries no reason field")
			}
			var reason string
			if err := json.Unmarshal(reasonRaw, &reason); err != nil {
				t.Fatalf("dev-2 reason is not a string: %v", err)
			}
			if reason != `device "router-2" is quarantined, not active` {
				t.Errorf("dev-2 reason = %q, want the recorded reason", reason)
			}
		default:
			t.Errorf("unexpected device_id %q in tasks", deviceID)
		}
	}
}
