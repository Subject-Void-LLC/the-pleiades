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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
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
