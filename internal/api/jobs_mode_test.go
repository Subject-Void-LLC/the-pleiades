// Package api_test: that a job's API responses say whether it was a check.
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// TestJobHandler_SaysWhetherAJobWasACheck covers the job record's mode,
// through a real store, on the detail and the list alike: a check reads
// "check", a job recorded before check mode existed reads "execute", and
// a record whose mode is not one reads "unreadable", never either of the
// two a reader could take it for.
func TestJobHandler_SaysWhetherAJobWasACheck(t *testing.T) {
	store := newTestJobStore(t)
	ctx := context.Background()

	want := map[string]string{}
	for mode, fields := range map[string]launch.Fields{
		"check":      {launch.ModeField: "check", "limit": "edge-*"},
		"execute":    {launch.ModeField: "execute"},
		"unreadable": {launch.ModeField: "Check"},
	} {
		job := &dispatch.Job{RunbookID: "patch-edge", Actor: "ada@example.com", Kind: "runbook", Fields: fields}
		if err := store.Create(ctx, job); err != nil {
			t.Fatalf("Create: %v", err)
		}
		want[job.JobID] = mode
	}
	legacy := &dispatch.Job{RunbookID: "patch-edge", Actor: "ada@example.com"}
	if err := store.Create(ctx, legacy); err != nil {
		t.Fatalf("Create: %v", err)
	}
	want[legacy.JobID] = "execute"

	router := jobsRouter(t, store)
	for jobID, mode := range want {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/jobs/"+jobID, nil))
		var body struct {
			Mode *string `json:"mode"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || rr.Code != http.StatusOK {
			t.Fatalf("GET job: status %d, %v: %s", rr.Code, err, rr.Body.String())
		}
		if body.Mode == nil || *body.Mode != mode {
			t.Errorf("job %s reads mode %v, want %q", jobID, body.Mode, mode)
		}
	}

	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/jobs", nil))
	var list struct {
		Jobs []struct {
			JobID string `json:"job_id"`
			Mode  string `json:"mode"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("GET jobs: status %d, %v: %s", rr.Code, err, rr.Body.String())
	}
	if len(list.Jobs) != len(want) {
		t.Fatalf("listed %d jobs, want %d", len(list.Jobs), len(want))
	}
	for _, j := range list.Jobs {
		if j.Mode != want[j.JobID] {
			t.Errorf("the list shows job %s as mode %q, want %q", j.JobID, j.Mode, want[j.JobID])
		}
	}
}

// TestJobHandler_SaysWhetherACheckWasComplete covers check_complete on the
// job response: false with the unchecked total and each device's count
// when a device left tasks unchecked, true when nothing was, and absent on
// a real run, so no reader takes "not a check" or "not finished" for an
// answer.
func TestJobHandler_SaysWhetherACheckWasComplete(t *testing.T) {
	store := newTestJobStore(t)
	ctx := context.Background()
	finish := func(mode string, unchecked int) string {
		t.Helper()
		job := &dispatch.Job{RunbookID: "patch-edge", Actor: "ada@example.com", Kind: "runbook", Fields: launch.Fields{launch.ModeField: mode}}
		if err := store.Create(ctx, job); err != nil {
			t.Fatal(err)
		}
		_, fence, err := store.BeginFanOut(ctx, job.JobID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: "dev-1", DeviceName: "router-1", Outcome: dispatch.OutcomeDispatched}); err != nil {
			t.Fatal(err)
		}
		if err := store.SettleRunning(ctx, job.JobID, fence, 1, 0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordResult(ctx, job.JobID, "dev-1", dispatch.ResultSucceeded, "", unchecked); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteRunning(ctx, job.JobID); err != nil {
			t.Fatal(err)
		}
		return job.JobID
	}

	router := jobsRouter(t, store)
	for _, tc := range []struct {
		name          string
		jobID         string
		wantComplete  *bool
		wantUnchecked int
	}{
		{"a check that left two tasks unchecked", finish("check", 2), ptr(false), 2},
		{"a check that checked everything", finish("check", 0), ptr(true), 0},
		{"a real run", finish("execute", 0), nil, 0},
	} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, api.APIVersionPrefix+"/jobs/"+tc.jobID, nil))
		var body struct {
			CheckComplete *bool `json:"check_complete"`
			Unchecked     int   `json:"unchecked"`
			Tasks         []struct {
				Unchecked int `json:"unchecked"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || rr.Code != http.StatusOK {
			t.Fatalf("%s: status %d, %v: %s", tc.name, rr.Code, err, rr.Body.String())
		}
		switch {
		case (body.CheckComplete == nil) != (tc.wantComplete == nil),
			body.CheckComplete != nil && *body.CheckComplete != *tc.wantComplete:
			t.Errorf("%s: check_complete = %v, want %v", tc.name, deref(body.CheckComplete), deref(tc.wantComplete))
		case body.Unchecked != tc.wantUnchecked || len(body.Tasks) != 1 || body.Tasks[0].Unchecked != tc.wantUnchecked:
			t.Errorf("%s: unchecked = %d (task %+v), want %d", tc.name, body.Unchecked, body.Tasks, tc.wantUnchecked)
		}
	}
}

// ptr returns a pointer to b.
func ptr(b bool) *bool { return &b }

// deref prints a *bool for a failure message.
func deref(b *bool) any {
	if b == nil {
		return "absent"
	}
	return *b
}
