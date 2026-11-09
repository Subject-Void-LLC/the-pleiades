// Tests for allow_skew on the Controller's rollback (dispatcher_rollback.go
// over internal/rollback's skew.go): the real planner and the real built-in
// catalog, with a journal whose directory entry is recorded under the
// declarations each case names.
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
	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fragment"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// directoryDeclarations is this Controller's identity of file.directory's
// group and its digest of the directory's undo through file.remove.
func directoryDeclarations(t *testing.T) (wire.CollectionIdentity, string) {
	t.Helper()
	id, err := collection.BuiltinIdentity("file.directory", buildinfo.Version())
	if err != nil {
		t.Fatal(err)
	}
	emitter, _ := collection.BuiltinManifest("file.directory")
	target, _ := collection.ManifestOf("file.remove")
	for _, spec := range emitter.Reversibility.Inverses {
		if spec.FQCN == "file.remove" {
			return id, collection.UndoDigest(spec, fragment.ByNameIn(target))
		}
	}
	t.Fatal("file.directory declares no undo through file.remove")
	return id, ""
}

// recordedUnder sets the fixture's directory entry (tasks[0]) to have been
// recorded under id and undo.
func (f *rollbackFixture) recordedUnder(id wire.CollectionIdentity, undo string) {
	e := &f.journal.entries[f.jobID][0]
	e.Collection, e.InverseSpecDigest = id, undo
}

// storedSteps returns the steps the rollback job jobID stored for dev-1.
func (f *rollbackFixture) storedSteps(t *testing.T, jobID string) []wire.RollbackStep {
	t.Helper()
	job, _, err := f.jobs.Get(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	steps, ok := job.Rollback.StepsFor("dev-1")
	if !ok {
		t.Fatalf("the rollback job %s plans nothing for dev-1: %+v", jobID, job.Rollback)
	}
	return steps
}

// TestRollback_AllowSkewMarksANodeTheControllerFindsUnskewed is the
// deadlock case: the Controller's declarations match the recording, a
// Runner's may not, so a node allow_skew names is planned marked accepted
// rather than refused as needless, and a Runner of other declarations can
// then run it.
func TestRollback_AllowSkewMarksANodeTheControllerFindsUnskewed(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	id, undo := directoryDeclarations(t)
	f.recordedUnder(id, undo)

	plain, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}})
	if err != nil {
		t.Fatalf("without allow_skew: %v", err)
	}
	if steps := f.storedSteps(t, plain); len(steps) != 1 || steps[0].SkewAccepted || steps[0].Collection != id || steps[0].UndoDigest != undo {
		t.Errorf("without allow_skew: steps %+v, want the undo unmarked and carrying its identity", steps)
	}

	named, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute,
		Leave: []string{"tasks[1]"}, AllowSkew: []string{"tasks[0]"}})
	if err != nil {
		t.Fatalf("allow_skew naming a node the Controller finds unskewed: %v", err)
	}
	if steps := f.storedSteps(t, named); len(steps) != 1 || !steps[0].SkewAccepted {
		t.Errorf("steps %+v, want the undo marked accepted for its Runner", steps)
	}
}

// TestRollback_ASkewedUndoIsRefusedUnderAllowSkewUntilNamed: the problem
// names the allow_skew field and the node; named, the step is marked.
func TestRollback_ASkewedUndoIsRefusedUnderAllowSkewUntilNamed(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	id, undo := directoryDeclarations(t)
	older := id
	older.Version, older.Manifests = "0.3.9", wire.ManifestDigestPrefix+strings.Repeat("a", 64)
	recorded := wire.ManifestDigestPrefix + strings.Repeat("b", 64)
	f.recordedUnder(older, recorded)

	_, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}})
	problems := refusal(t, err)
	if len(problems) != 1 || problems[0].Field != "allow_skew" || problems[0].Value != "tasks[0]" ||
		!strings.Contains(problems[0].Reason, recorded) || !strings.Contains(problems[0].Reason, undo) {
		t.Fatalf("problems %+v, want one accepted by allow_skew tasks[0], naming both undo digests", problems)
	}
	if f.bus.count() != 0 {
		t.Error("a refused rollback published a launch")
	}

	id2, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute,
		Leave: []string{"tasks[1]"}, AllowSkew: []string{"tasks[0]"}})
	if err != nil {
		t.Fatalf("with allow_skew: %v", err)
	}
	if steps := f.storedSteps(t, id2); len(steps) != 1 || !steps[0].SkewAccepted || steps[0].UndoDigest != recorded {
		t.Errorf("steps %+v, want the recorded undo marked accepted", steps)
	}
}

// TestRollback_AllowSkewNamingANodeTheJournalLacksIsRefused: a name that
// matches no entry accepts nothing, here as on the CLI.
func TestRollback_AllowSkewNamingANodeTheJournalLacksIsRefused(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	_, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute,
		Leave: []string{"tasks[1]"}, AllowSkew: []string{"tasks[9]"}})
	problems := refusal(t, err)
	if len(problems) != 1 || problems[0].Node != "tasks[9]" || problems[0].Field != "" {
		t.Errorf("problems %+v, want one for tasks[9] that no field accepts", problems)
	}
}

// TestRollbackJob_AnswersWithThePlansWarnings: the 202 body carries what
// the plan says to read, always as a list: a recorded undo checked against
// this Controller's declarations alone, and a skew allow_skew accepted,
// naming the digests compared.
func TestRollbackJob_AnswersWithThePlansWarnings(t *testing.T) {
	type warning struct {
		Node    string `json:"node"`
		Device  string `json:"device"`
		Message string `json:"message"`
	}
	post := func(t *testing.T, f *rollbackFixture, body string) []warning {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/jobs/"+f.jobID+"/rollback", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", f.jobID)
		req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		f.dispatcher.RollbackJob(rec, req)
		var accepted struct {
			JobID    string     `json:"job_id"`
			Warnings *[]warning `json:"warnings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil || rec.Code != http.StatusAccepted || accepted.Warnings == nil {
			t.Fatalf("POST %s: %d %s, want 202 with a warnings list", body, rec.Code, rec.Body)
		}
		return *accepted.Warnings
	}

	f := newRollbackFixture(t, nil, nil)
	got := post(t, f, `{"leave": ["tasks[1]"]}`)
	if len(got) != 1 || got[0].Node != "tasks[0]" || got[0].Device != "web1" || !strings.Contains(got[0].Message, "no Collection identity") {
		t.Errorf("warnings %+v, want one for tasks[0] on web1 saying it was recorded with no Collection identity", got)
	}

	f = newRollbackFixture(t, nil, nil)
	id, undo := directoryDeclarations(t)
	older := id
	older.Version, older.Manifests = "0.3.9", wire.ManifestDigestPrefix+strings.Repeat("a", 64)
	recorded := wire.ManifestDigestPrefix + strings.Repeat("b", 64)
	f.recordedUnder(older, recorded)
	got = post(t, f, `{"leave": ["tasks[1]"], "allow_skew": ["tasks[0]"]}`)
	if len(got) != 1 || !strings.Contains(got[0].Message, "allow_skew accepted") || !strings.Contains(got[0].Message, recorded) || !strings.Contains(got[0].Message, undo) {
		t.Errorf("warnings %+v, want one naming the undo digests allow_skew accepted", got)
	}
}

// TestRollbackJob_TakesAllowSkew: the request field reaches the planner.
func TestRollbackJob_TakesAllowSkew(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	id, undo := directoryDeclarations(t)
	f.recordedUnder(id, undo)
	req := httptest.NewRequest(http.MethodPost, "/jobs/"+f.jobID+"/rollback", strings.NewReader(`{"leave": ["tasks[1]"], "allow_skew": ["tasks[0]"]}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", f.jobID)
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	f.dispatcher.RollbackJob(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST with allow_skew: %d %s", rec.Code, rec.Body)
	}
	jobID := strings.TrimPrefix(rec.Header().Get("Location"), api.APIVersionPrefix+"/jobs/")
	if steps := f.storedSteps(t, jobID); len(steps) != 1 || !steps[0].SkewAccepted {
		t.Errorf("steps %+v, want the undo allow_skew named marked accepted", steps)
	}
}
