// Package api_test: JobHandler.Cancel, in its own sibling file per this
// repository's file-per-concern convention.
//
// Every case drives the real chi router jobsRouterWithCanceler builds,
// mounting the route from apispec.CancelJob's own declaration, and every
// store-backed case uses the real ent-backed dispatch.JobStore rather than
// a double. Per AGENTS.md RULE 0, a cancel test standing on a fake store
// would prove the handler calls a method, not that a job stops.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/go-chi/chi/v5"
)

// cancelPath builds the URL under test, so no case restates the pattern.
func cancelPath(jobID string) string {
	return api.APIVersionPrefix + "/jobs/" + jobID + "/cancel"
}

// postCancel drives one cancel request through router and returns the
// recorder.
func postCancel(t *testing.T, router http.Handler, jobID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, cancelPath(jobID), nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// TestJobHandler_CancelStopsARunningJob is the happy path, asserted on the
// stored record rather than only on the status code: a 202 that had not
// actually canceled anything would pass a status-only check.
func TestJobHandler_CancelStopsARunningJob(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "launcher@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := store.BeginFanOut(ctx, job.JobID, time.Hour); err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}

	router := jobsRouterWithCanceler(t, store, store)
	rr := postCancel(t, router, job.JobID)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusAccepted, rr.Body.String())
	}

	var body struct {
		JobID string `json:"job_id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode the response body: %v", err)
	}
	if body.State != "canceled" {
		t.Errorf("response state = %q, want %q: the body must be the job as it now stands, not as it was", body.State, "canceled")
	}

	stored, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State != "canceled" {
		t.Errorf("stored State = %q, want %q", stored.State, "canceled")
	}
	// The caller's identity, not the job's own actor. Stopping a run is a
	// new decision by whoever made it, and stamping the launcher would put
	// somebody else's name on a choice they did not make.
	if stored.CanceledBy != "test-user" {
		t.Errorf("CanceledBy = %q, want the caller's own subject %q, not the launcher's", stored.CanceledBy, "test-user")
	}
	if stored.Actor != "launcher@example.com" {
		t.Errorf("Actor = %q, want the original launcher unchanged", stored.Actor)
	}
}

// TestJobHandler_CancelFinishedJobIsConflict proves a job that has already
// stopped is refused rather than reported as stopped. Answering 202 here
// would tell a caller it had done something it had not.
func TestJobHandler_CancelFinishedJobIsConflict(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "launcher@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	if err := store.Complete(ctx, job.JobID, fence, 1, 0, 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	router := jobsRouterWithCanceler(t, store, store)
	rr := postCancel(t, router, job.JobID)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d for an already-finished job: %s", rr.Code, http.StatusConflict, rr.Body.String())
	}

	stored, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State != "completed" {
		t.Errorf("stored State after the refused cancel = %q, want %q unchanged", stored.State, "completed")
	}
}

// TestJobHandler_CancelUnknownJobIs404 proves an id naming no job is
// distinguishable from one naming a job that cannot be stopped. A caller
// has to be able to tell "you got the id wrong" from "it already
// finished", and both would otherwise be a bare refusal.
func TestJobHandler_CancelUnknownJobIs404(t *testing.T) {
	store := newTestJobStore(t)
	router := jobsRouterWithCanceler(t, store, store)

	rr := postCancel(t, router, "00000000-0000-0000-0000-000000000000")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

// TestJobHandler_CancelRejectsNonUUID mirrors Get's own validation. The id
// is not concatenated into a subject on this path, but accepting one shape
// of id here and refusing it one route over is the drift that makes a
// validation rule stop being a rule.
func TestJobHandler_CancelRejectsNonUUID(t *testing.T) {
	store := newTestJobStore(t)
	router := jobsRouterWithCanceler(t, store, store)

	for _, id := range []string{"not-a-uuid", "..", "%3E", "1"} {
		t.Run(id, func(t *testing.T) {
			rr := postCancel(t, router, id)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("status for id %q = %d, want %d", id, rr.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestJobHandler_CancelUnwiredIsNotImplemented proves a Controller that
// wires no canceller answers rather than panicking.
//
// This is not hypothetical: internal/ui/resources's own conformance
// harness builds its dependencies with collaborators left nil, and a
// handler that dereferenced one would take the process down rather than
// return a status.
func TestJobHandler_CancelUnwiredIsNotImplemented(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "launcher@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}

	router := jobsRouterWithCanceler(t, store, nil)
	rr := postCancel(t, router, job.JobID)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status with no canceller wired = %d, want %d", rr.Code, http.StatusNotImplemented)
	}
}

// The real cancel path must satisfy this package's port. Asserted at
// compile time here rather than left to cmd/controller to discover,
// because the failure mode is a change to one consumer's interface
// silently leaving the other behind, and the browser takes the identical
// implementation through internal/ui/resources/jobs.Canceler.
var _ api.JobCanceler = (*dispatch.Canceller)(nil)

// unauthenticated is an Auth middleware that passes the request through
// without putting an identity on its context, which is what a route
// reached outside the real authentication chain looks like.
func unauthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// failingCanceler always refuses with an error that is neither sentinel,
// standing in for a backing store that is briefly unavailable.
type failingCanceler struct{}

func (failingCanceler) Cancel(_ context.Context, _ string, _ string) error {
	return errors.New("deliberate store failure naming a table nobody should read")
}

// succeedingCanceler always reports the job stopped, for the case where
// the write lands and the read back does not.
type succeedingCanceler struct{}

func (succeedingCanceler) Cancel(_ context.Context, _ string, _ string) error { return nil }

// TestCancelRoute_RejectsAnAnonymousCallerBeforeTheHandler proves the
// mounted route is closed to a request carrying no identity.
//
// The rejection here comes from the authorization middleware, not from the
// handler: the body is its generic "unauthorized" rather than the
// handler's own sentence, and that distinction is asserted rather than
// assumed. An earlier version of this test checked only the status code
// and so proved nothing about which layer refused, while appearing to test
// the handler's own guard.
func TestCancelRoute_RejectsAnAnonymousCallerBeforeTheHandler(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "launcher@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}

	router := jobsRouterWithAuth(t, store, store, unauthenticated)
	rr := postCancel(t, router, job.JobID)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unauthorized") {
		t.Errorf("body = %q, want the middleware's own refusal: this test is about the route being closed, not about the handler", rr.Body.String())
	}

	stored, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State == "canceled" {
		t.Error("the job was canceled by a request carrying no identity")
	}
}

// TestJobHandler_CancelWithoutAnIdentityRefuses covers the handler's own
// guard, which the route above never lets a request reach.
//
// It is called directly, with a chi route context supplying the URL
// parameter the router would have, because that guard is unreachable
// through the mounted route by design. Defensive is not the same as dead:
// the handler is an exported method on an exported type, the scope
// middleware is configuration rather than a type-system guarantee, and a
// cancel attributed to nobody would leave a terminal record asserting that
// somebody decided this while naming no one. The test also pins the
// handler's own message, so it cannot be confused with the middleware's.
func TestJobHandler_CancelWithoutAnIdentityRefuses(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "launcher@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}

	handler := api.NewJobHandler(store, store)
	req := httptest.NewRequest(http.MethodPost, cancelPath(job.JobID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", job.JobID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	handler.Cancel(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "identity") {
		t.Errorf("body = %q, want the handler's own refusal naming the missing identity", rr.Body.String())
	}

	stored, _, err := store.Get(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.State == "canceled" {
		t.Error("the handler canceled a job it could not attribute to anybody")
	}
}

// TestJobHandler_CancelStoreFailureIs500AndSaysNothing proves a store
// failure that is neither sentinel becomes a generic 500, and that the
// store's own text never reaches the caller.
//
// That text can name tables and columns, which somebody holding
// runbook:execute has no business reading. This mirrors the identical
// assertion the Get handler's own store-failure test already makes.
func TestJobHandler_CancelStoreFailureIs500AndSaysNothing(t *testing.T) {
	store := newTestJobStore(t)
	router := jobsRouterWithCanceler(t, store, failingCanceler{})

	rr := postCancel(t, router, "3b6a1c52-2b8f-4a41-9c21-8f9a2d0b7e11")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "table") {
		t.Errorf("body = %q, want it to carry none of the store's own wording", rr.Body.String())
	}
}

// TestJobHandler_CancelAnswersEvenWhenTheReadBackFails proves a cancel
// that landed is still reported as having landed.
//
// The job is stopped by the time the handler reaches its read back, so
// answering an error here would be false, and would invite a retry that
// would then be refused with a 409 while the job stayed canceled. The
// caller gets the id and the state instead of a full record.
func TestJobHandler_CancelAnswersEvenWhenTheReadBackFails(t *testing.T) {
	router := jobsRouterWithCanceler(t, erroringJobRepository{}, succeedingCanceler{})

	rr := postCancel(t, router, "3b6a1c52-2b8f-4a41-9c21-8f9a2d0b7e11")
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d: a cancel that succeeded must not report a failure because the read back did", rr.Code, http.StatusAccepted)
	}

	var body struct {
		JobID string `json:"job_id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode the response body: %v", err)
	}
	if body.State != "canceled" {
		t.Errorf("state = %q, want %q", body.State, "canceled")
	}
	if body.JobID == "" {
		t.Error("the fallback body carried no job id, so a caller could not tell which job it refers to")
	}
}

// TestJobHandler_CancelReportsWhenEachDeviceFinished covers the timestamp
// a client polling a running job reads to see what it is still waiting on.
func TestJobHandler_CancelReportsWhenEachDeviceFinished(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)

	job := &dispatch.Job{RunbookID: "pb-1", Actor: "launcher@example.com"}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
	if err != nil {
		t.Fatalf("BeginFanOut: %v", err)
	}
	if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{
		DeviceID: "dev-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched,
	}); err != nil {
		t.Fatalf("RecordTask: %v", err)
	}
	if err := store.SettleRunning(ctx, job.JobID, fence, 1, 0, 0); err != nil {
		t.Fatalf("SettleRunning: %v", err)
	}
	if _, err := store.RecordResult(ctx, job.JobID, "dev-1", dispatch.ResultSucceeded, "", 0); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}

	router := jobsRouterWithCanceler(t, store, store)
	req := httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/jobs/"+job.JobID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var body struct {
		Tasks []struct {
			Result     string `json:"result"`
			FinishedAt string `json:"finished_at"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode the response body: %v", err)
	}
	if len(body.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(body.Tasks))
	}
	if body.Tasks[0].Result != "succeeded" {
		t.Errorf("result = %q, want %q", body.Tasks[0].Result, "succeeded")
	}
	if body.Tasks[0].FinishedAt == "" {
		t.Error("finished_at is absent for a device that reported back, so a client cannot tell it apart from one still running")
	}
}
