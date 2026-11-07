// Tests that a refused rollback tells the caller which kind of later job
// stands in its way and which methods failed during their action there,
// as the command line's --json does.
package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
)

// TestRollbackJob_NamesALaterJobsKindAndWhatFailedThere: a later job whose
// task failed during its action on web1 may have changed it, so the
// refusal carries kind unknown_effect and names the method in
// unknown_effects, beside the field and value that accept it.
func TestRollbackJob_NamesALaterJobsKindAndWhatFailedThere(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	later := f.journal.entries[f.jobID][1]
	later.JobID, later.RunID = "later-job", "run-2"
	later.Outcome, later.ActionChanged, later.FailureStage = engine.OutcomeFailed, false, engine.FailureStageAction
	later.InverseFQCN, later.InverseParamKeys, later.InverseParams, later.InverseComplete = "", nil, nil, false
	later.StartedAt, later.FinishedAt = time.Now(), time.Now().Add(time.Second)
	f.journal.entries["later-job"] = []engine.JournalEntry{later}

	req := httptest.NewRequest(http.MethodPost, "/jobs/"+f.jobID+"/rollback", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", f.jobID)
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	f.dispatcher.RollbackJob(rec, req)

	var refused struct {
		Problems []map[string]any `json:"problems"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil || rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no refusal: %d %s", rec.Code, rec.Body)
	}
	var found bool
	for _, p := range refused.Problems {
		if p["field"] != "despite_job" {
			continue
		}
		found = true
		effects, _ := p["unknown_effects"].([]any)
		if p["value"] != "later-job" || p["kind"] != rollback.KindUnknownEffect || !slices.Equal(effects, []any{"file.line.set"}) {
			t.Errorf("the later job's problem = %v, want kind %s naming file.line.set in unknown_effects", p, rollback.KindUnknownEffect)
		}
	}
	if !found {
		t.Fatalf("no problem over the later job in %s", rec.Body)
	}
	// A problem of another sort carries neither.
	for _, p := range refused.Problems {
		if p["field"] == "leave" {
			if _, ok := p["kind"]; ok {
				t.Errorf("a leave problem carries a kind: %v", p)
			}
			if _, ok := p["unknown_effects"]; ok {
				t.Errorf("a leave problem carries unknown_effects: %v", p)
			}
		}
	}
}
