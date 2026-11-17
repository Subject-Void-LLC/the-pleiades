// Tests that POST /jobs/{id}/rollback names each cleanup a block's always:
// ran that the plan leaves in place, in its answer, and in its refusal when
// nothing else is left to run: before, the Controller left them in place
// and said nothing.
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
)

// cleanupRan makes a directory in a block and removes a scratch file in the
// block's always:.
const cleanupRan = `id: rb-1
tasks:
  - block:
      - name: make a directory
        file.directory:
          path: /tmp/made
    always:
      - name: tear the scratch down
        exec.command:
          argv: [rm, -rf, /tmp/scratch]
`

// newCleanupFixture is a finished job of cleanupRan whose journal holds the
// always: task's change, and the block's change with its undo when made.
func newCleanupFixture(t *testing.T, made bool) *rollbackFixture {
	t.Helper()
	return newRollbackFixtureOf(t, nil, nil, cleanupRan, func(entry func(node, fqcn string, seq int) engine.JournalEntry) []engine.JournalEntry {
		var out []engine.JournalEntry
		if made {
			e := entry("tasks[0].block[0]", "file.directory", 1)
			e.InverseFQCN, e.InverseParamKeys, e.InverseComplete = "file.remove", []string{"path"}, true
			e.InverseParams = []engine.InverseParam{{Key: "path", Text: text("/tmp/made")}}
			out = append(out, e)
		}
		return append(out, entry("tasks[0].always[0]", "exec.command", 2))
	})
}

// postRollback calls the route for f's job with body.
func postRollback(t *testing.T, f *rollbackFixture, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/jobs/"+f.jobID+"/rollback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", f.jobID)
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	f.dispatcher.RollbackJob(rec, req)
	return rec
}

// TestRollbackJob_NamesTheCleanupsItLeavesInPlace: an accepted rollback
// answers with each cleanup it leaves, and its job holds no step for one;
// a job with nothing but a cleanup is refused naming it; and an accepted
// rollback with none answers with an empty list, never an absent one.
func TestRollbackJob_NamesTheCleanupsItLeavesInPlace(t *testing.T) {
	f := newCleanupFixture(t, true)
	rec := postRollback(t, f, `{}`)
	var accepted struct {
		JobID    string                `json:"job_id"`
		Cleanups []api.RollbackCleanup `json:"cleanups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil || rec.Code != http.StatusAccepted {
		t.Fatalf("accepted: %d %s", rec.Code, rec.Body)
	}
	if len(accepted.Cleanups) != 1 || accepted.Cleanups[0].Node != "tasks[0].always[0]" || accepted.Cleanups[0].Device != "web1" ||
		!strings.Contains(accepted.Cleanups[0].Reason, "always:") {
		t.Errorf("cleanups %+v, want the always: task on web1 with its reason", accepted.Cleanups)
	}
	job, _, err := f.jobs.Get(t.Context(), accepted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if steps := job.Rollback.Devices[0].Steps; len(steps) != 1 || steps[0].Node != "tasks[0].block[0]" {
		t.Errorf("the rollback's steps %+v, want only the block's", steps)
	}

	only := newCleanupFixture(t, false)
	rec = postRollback(t, only, `{}`)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "a cleanup a block's always: ran") || !strings.Contains(rec.Body.String(), "tasks[0].always[0] on web1") {
		t.Errorf("nothing but a cleanup: %d %s, want a refusal naming it", rec.Code, rec.Body)
	}

	plain := newRollbackFixture(t, nil, nil)
	rec = postRollback(t, plain, `{"leave": ["tasks[1]"]}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"cleanups":[]`) {
		t.Errorf("no cleanup: %d %s, want an empty list", rec.Code, rec.Body)
	}
}
