// Tests for the Controller's rollback (dispatcher_rollback.go): the real
// planner, the real runbook source and ent job store, and the real
// built-in catalog, with a journal handed in as the store would return it.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // the built-in methods the journal names
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// rollbackRan is the runbook the job being undone ran.
const rollbackRan = `id: rb-1
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/made
  - name: edit a file
    file.line.set:
      path: /tmp/conf
      line: "a = b"
`

// journalStub is the stored journal, as RollbackJournal returns it.
type journalStub struct {
	entries map[string][]engine.JournalEntry
}

func (j journalStub) AllForJob(_ context.Context, jobID string) ([]engine.JournalEntry, error) {
	return j.entries[jobID], nil
}

func (j journalStub) OnDevicesSince(_ context.Context, jobID string, devices []string, since time.Time) ([]engine.JournalEntry, error) {
	var out []engine.JournalEntry
	for id, entries := range j.entries {
		if id == jobID {
			continue
		}
		for _, e := range entries {
			for _, d := range devices {
				if e.DeviceID == d && e.FinishedAt.After(since) {
					out = append(out, e)
				}
			}
		}
	}
	return out, nil
}

// rollbackFixture is a finished job over device dev-1 ("web1") with its
// journal: a directory made, with its undo recorded, and a file edited,
// whose undo withholds the content.
type rollbackFixture struct {
	jobs       dispatch.JobStore
	bus        *capturingBus
	source     runbook.Source
	dir        string
	journal    journalStub
	jobID      string
	version    string
	dispatcher *api.Dispatcher
}

func text(s string) *string { return &s }

func newRollbackFixture(t *testing.T, tmpl *launch.Template, extraVars map[string]any) *rollbackFixture {
	t.Helper()
	f := &rollbackFixture{jobs: newTestJobStore(t), bus: newCapturingBus(), dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(f.dir, "rb-1.yaml"), []byte(rollbackRan), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	if f.source, err = runbook.NewDirSource(f.dir); err != nil {
		t.Fatal(err)
	}
	dag, err := f.source.GetDAG(t.Context(), "rb-1")
	if err != nil {
		t.Fatal(err)
	}
	f.version = dag.Version

	job := &dispatch.Job{RunbookID: "rb-1", GroupName: "web", Actor: "ada@example.com", Kind: "runbook", ExtraVars: extraVars}
	if tmpl != nil {
		job.TemplateID = tmpl.ID
	}
	f.finish(t, job, true)
	f.jobID = job.JobID

	start := time.Now().Add(-time.Hour)
	entry := func(node, fqcn string, seq int) engine.JournalEntry {
		return engine.JournalEntry{JobID: job.JobID, RunID: "run-1", NodeID: node, DeviceID: "dev-1", Sequence: seq, FQCN: fqcn,
			Outcome: engine.OutcomeChanged, ActionChanged: true, DAGID: "rb-1", DAGVersion: dag.Version,
			StartedAt: start.Add(time.Duration(seq) * time.Second), FinishedAt: start.Add(time.Duration(seq)*time.Second + time.Millisecond)}
	}
	made := entry("tasks[0]", "file.directory", 1)
	made.InverseFQCN, made.InverseParamKeys, made.InverseComplete = "file.remove", []string{"path"}, true
	made.InverseParams = []engine.InverseParam{{Key: "path", Text: text("/tmp/made")}}
	edited := entry("tasks[1]", "file.line.set", 2)
	edited.InverseFQCN, edited.InverseParamKeys = "file.copy", []string{"content", "dest"}
	edited.InverseParams = []engine.InverseParam{{Key: "dest", Text: text("/tmp/conf")}}
	f.journal = journalStub{entries: map[string][]engine.JournalEntry{job.JobID: {made, edited}}}

	opts := []api.DispatcherOption{api.WithRollback(&f.journal, func(_ context.Context, ids []string) (map[string]string, error) {
		return map[string]string{"dev-1": "web1"}, nil
	})}
	if tmpl != nil {
		opts = append(opts, api.WithTemplates(stubTemplates{tmpl: *tmpl}))
	}
	f.dispatcher = api.NewDispatcher(f.source, f.jobs, f.bus, opts...)
	return f
}

// finish stores job and takes it through a fan-out to dev-1 and, when
// reported, that device's result.
func (f *rollbackFixture) finish(t *testing.T, job *dispatch.Job, reported bool) {
	t.Helper()
	ctx := t.Context()
	if err := f.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	_, fence, err := f.jobs.BeginFanOut(ctx, job.JobID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.jobs.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: "dev-1", DeviceName: "web1", Outcome: dispatch.OutcomeDispatched}); err != nil {
		t.Fatal(err)
	}
	if !reported {
		if err := f.jobs.Complete(ctx, job.JobID, fence, 1, 0, 0); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := f.jobs.SettleRunning(ctx, job.JobID, fence, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.RecordResult(ctx, job.JobID, "dev-1", dispatch.ResultSucceeded, "", 0); err != nil {
		t.Fatal(err)
	}
	if err := f.jobs.CompleteRunning(ctx, job.JobID); err != nil {
		t.Fatal(err)
	}
}

// refusal returns a refused rollback's problems, failing on anything else.
func refusal(t *testing.T, err error) []api.RollbackProblem {
	t.Helper()
	var refused *api.RollbackRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Rollback = %v, want a refusal", err)
	}
	return refused.Problems
}

func TestRollback_RefusesAWithheldUndoUntilItIsLeft(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	_, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute})
	problems := refusal(t, err)
	if len(problems) != 1 || problems[0].Node != "tasks[1]" || problems[0].Field != "leave" || problems[0].Value != "tasks[1]" || problems[0].Device != "web1" {
		t.Fatalf("problems %+v, want one for tasks[1] on web1 accepted by leave", problems)
	}
	if f.bus.count() != 0 {
		t.Fatal("a refused rollback published a launch")
	}

	id, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeCheck, Leave: []string{"tasks[1]"}})
	if err != nil {
		t.Fatalf("with leave: %v", err)
	}
	job, _, err := f.jobs.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if job.RollbackOf != f.jobID || job.Actor != "grace@example.com" || job.RunbookID != "rb-1" || job.Fields[launch.ModeField] != "check" {
		t.Errorf("the rollback job %+v", job)
	}
	if job.Rollback == nil || job.Rollback.DAGVersion != f.version || len(job.Rollback.Devices) != 1 {
		t.Fatalf("the rollback plan %+v", job.Rollback)
	}
	d := job.Rollback.Devices[0]
	if d.DeviceID != "dev-1" || d.DeviceName != "web1" || len(d.Steps) != 1 || d.Steps[0].Method != "file.remove" || d.Steps[0].Params["path"] != "/tmp/made" || d.Steps[0].Emitter != "file.directory" {
		t.Errorf("dev-1's steps %+v, want the directory's recorded undo", d)
	}
	if f.bus.count() != 1 {
		t.Errorf("published %d launches, want 1", f.bus.count())
	}

	// A rollback is not itself undone.
	if _, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", id, api.RollbackRequest{Mode: collection.ModeExecute}); !errors.Is(err, api.ErrNotRollbackable) {
		t.Errorf("a rollback of a rollback: %v", err)
	}
}

// TestRollback_HoldsTheJournalToTheRunbookThatRan covers a journal that
// does not agree with the runbook: rows naming another method at a node,
// and a runbook changed since.
func TestRollback_HoldsTheJournalToTheRunbookThatRan(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	req := api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}}

	// A row claiming tasks[0] ran file.touch, whose recorded undo passes
	// the registry's check on its own.
	f.journal.entries[f.jobID][0].FQCN = "file.touch"
	problems := refusal(t, func() error { _, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, req); return err }())
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "runs file.directory") || problems[0].Field != "" {
		t.Fatalf("problems %+v, want the node's method named and nothing that accepts it", problems)
	}
	f.journal.entries[f.jobID][0].FQCN = "file.directory"

	// Adding a rollback: list is not a change to the runbook...
	withList := strings.Replace(rollbackRan, "      line: \"a = b\"\n", "      line: \"a = b\"\n    rollback:\n      - name: put it back\n        file.line.set:\n          path: /tmp/conf\n          line: \"a = c\"\n", 1)
	if err := os.WriteFile(filepath.Join(f.dir, "rb-1.yaml"), []byte(withList), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	_ = os.Chtimes(filepath.Join(f.dir, "rb-1.yaml"), future, future)
	id, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute})
	if err != nil {
		t.Fatalf("with a rollback: list written since: %v", err)
	}
	job, _, _ := f.jobs.Get(t.Context(), id)
	if steps := job.Rollback.Devices[0].Steps; len(steps) != 2 || steps[0].Source != "authored" || steps[0].Params["line"] != "a = c" {
		t.Errorf("steps %+v, want the list written since, then the recorded undo", steps)
	}

	// ...and changing what it runs is.
	changed := strings.Replace(rollbackRan, "make a directory", "make the directory", 1)
	if err := os.WriteFile(filepath.Join(f.dir, "rb-1.yaml"), []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	future = future.Add(time.Minute)
	_ = os.Chtimes(filepath.Join(f.dir, "rb-1.yaml"), future, future)
	problems = refusal(t, func() error { _, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, req); return err }())
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "has changed since") {
		t.Errorf("problems %+v, want the changed runbook named", problems)
	}
}

func TestRollback_RefusesWhatNoRequestCanRollBack(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	for name, job := range map[string]*dispatch.Job{
		"a check":        {RunbookID: "rb-1", GroupName: "web", Actor: "a", Kind: "runbook", Fields: launch.Fields{launch.ModeField: "check"}},
		"a playbook job": {RunbookID: "rb-1", GroupName: "web", Actor: "a", Kind: "playbook"},
	} {
		f.finish(t, job, true)
		if _, err := f.dispatcher.Rollback(t.Context(), "g", job.JobID, api.RollbackRequest{Mode: collection.ModeExecute}); !errors.Is(err, api.ErrNotRollbackable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	running := &dispatch.Job{RunbookID: "rb-1", GroupName: "web", Actor: "a", Kind: "runbook"}
	if err := f.jobs.Create(t.Context(), running); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dispatcher.Rollback(t.Context(), "g", running.JobID, api.RollbackRequest{Mode: collection.ModeExecute}); !errors.Is(err, api.ErrNotRollbackable) {
		t.Errorf("a job not yet finished: %v", err)
	}
	if _, err := f.dispatcher.Rollback(t.Context(), "g", "5f0e4ac2-0000-4000-8000-000000000000", api.RollbackRequest{}); !errors.Is(err, dispatch.ErrJobNotFound) {
		t.Errorf("no such job: %v", err)
	}
}

// TestRollback_AnUnreportedDeviceIsUnknown covers a job some device never
// reported back from: its last level may be missing from the journal.
func TestRollback_AnUnreportedDeviceIsUnknown(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	cut := &dispatch.Job{RunbookID: "rb-1", GroupName: "web", Actor: "a", Kind: "runbook"}
	f.finish(t, cut, false)
	f.journal.entries[cut.JobID] = f.journal.entries[f.jobID]
	for i := range f.journal.entries[cut.JobID] {
		f.journal.entries[cut.JobID][i].JobID = cut.JobID
	}
	delete(f.journal.entries, f.jobID)
	req := api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}}
	problems := refusal(t, func() error { _, err := f.dispatcher.Rollback(t.Context(), "g", cut.JobID, req); return err }())
	if len(problems) != 1 || problems[0].Field != "allow_unknown" || problems[0].Value != "unsealed" {
		t.Fatalf("problems %+v, want the missing report accepted by allow_unknown unsealed", problems)
	}
	req.AllowUnknown = []string{"unsealed"}
	if _, err := f.dispatcher.Rollback(t.Context(), "g", cut.JobID, req); err != nil {
		t.Errorf("accepted: %v", err)
	}
}

// TestRollback_NeverReplaysASecretAnswer proves the rollback job carries
// the undone job's extra variables without a survey password.
func TestRollback_NeverReplaysASecretAnswer(t *testing.T) {
	tmpl := launchableTemplate()
	tmpl.Definition = "rb-1"
	tmpl.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "db_password", Label: "password", Type: launch.QuestionPassword},
	}}
	f := newRollbackFixture(t, &tmpl, map[string]any{"db_password": "hunter2", "region": "west"})
	id, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}})
	if err != nil {
		t.Fatal(err)
	}
	job, _, _ := f.jobs.Get(t.Context(), id)
	if _, kept := job.ExtraVars["db_password"]; kept || job.ExtraVars["region"] != "west" {
		t.Errorf("the rollback job's extra variables are %v, want region without the password", job.ExtraVars)
	}
	raw, _ := json.Marshal(job)
	if strings.Contains(string(raw), "hunter2") {
		t.Error("the password reached the rollback job")
	}
}

// TestRollbackJob_AnswersEveryProblemWithTheFieldThatAcceptsIt drives the
// HTTP handler: a refusal is a 422 listing the problems, and an accepted
// rollback a 202 naming the new job.
func TestRollbackJob_AnswersEveryProblemWithTheFieldThatAcceptsIt(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/jobs/"+f.jobID+"/rollback", strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", f.jobID)
		req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()
		f.dispatcher.RollbackJob(rec, req)
		return rec
	}

	rec := call("")
	var refused struct {
		Status   string                `json:"status"`
		Problems []api.RollbackProblem `json:"problems"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil || rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no body: %d %s", rec.Code, rec.Body)
	}
	if refused.Status != "refused" || len(refused.Problems) != 1 || refused.Problems[0].Field != "leave" {
		t.Errorf("refusal %+v", refused)
	}
	if rec := call(`{"mode": "now"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown mode: %d", rec.Code)
	}
	if rec := call(`{"leave": ["tasks[1]"], "extra": 1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown field: %d", rec.Code)
	}
	rec = call(`{"leave": ["tasks[1]"]}`)
	var accepted struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil || rec.Code != http.StatusAccepted || accepted.JobID == "" {
		t.Fatalf("accepted: %d %s", rec.Code, rec.Body)
	}
	if loc := rec.Header().Get("Location"); !strings.HasSuffix(loc, "/jobs/"+accepted.JobID) {
		t.Errorf("Location %q", loc)
	}
}

// TestRollback_RefusesUndoingBeneathALaterJob covers another job that
// changed the same device after this one began: undoing beneath it is
// refused, named by despite_job, until the request names it.
func TestRollback_RefusesUndoingBeneathALaterJob(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	later := f.journal.entries[f.jobID][0]
	later.JobID, later.RunID = "later-job", "run-2"
	later.StartedAt, later.FinishedAt = time.Now(), time.Now().Add(time.Second)
	f.journal.entries["later-job"] = []engine.JournalEntry{later}

	req := api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}}
	problems := refusal(t, func() error { _, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, req); return err }())
	if len(problems) != 1 || problems[0].Field != "despite_job" || problems[0].Value != "later-job" || !strings.Contains(problems[0].Reason, "web1") {
		t.Fatalf("problems %+v, want the later job named, accepted by despite_job", problems)
	}
	req.DespiteJobs = []string{"later-job"}
	if _, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, req); err != nil {
		t.Errorf("named: %v", err)
	}
}

// failingJournal is a journal store that cannot be read.
type failingJournal struct {
	all, others error
	entries     []engine.JournalEntry
}

func (j failingJournal) AllForJob(context.Context, string) ([]engine.JournalEntry, error) {
	return j.entries, j.all
}

func (j failingJournal) OnDevicesSince(context.Context, string, []string, time.Time) ([]engine.JournalEntry, error) {
	return nil, j.others
}

// TestRollback_ReportsWhatItCouldNotRead covers every read a plan depends
// on failing, and the job shapes no plan can come from.
func TestRollback_ReportsWhatItCouldNotRead(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	entries := f.journal.entries[f.jobID]
	names := func(context.Context, []string) (map[string]string, error) {
		return map[string]string{"dev-1": "web1"}, nil
	}
	req := api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}}
	rollback := func(opts ...api.DispatcherOption) error {
		_, err := api.NewDispatcher(f.source, f.jobs, f.bus, opts...).Rollback(t.Context(), "g", f.jobID, req)
		return err
	}
	boom := errors.New("the store is down")

	if err := rollback(); err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Errorf("an unwired controller: %v", err)
	}
	for name, tc := range map[string]struct {
		journal api.RollbackJournal
		names   api.DeviceNamer
		want    error
		text    string
	}{
		"the job's journal":     {failingJournal{all: boom}, names, boom, ""},
		"nothing journaled":     {failingJournal{}, names, api.ErrNotRollbackable, "journaled nothing"},
		"other jobs' journals":  {failingJournal{entries: entries, others: boom}, names, boom, ""},
		"the devices' names":    {failingJournal{entries: entries}, func(context.Context, []string) (map[string]string, error) { return nil, boom }, boom, ""},
		"two runbook versions":  {failingJournal{entries: mixedVersions(entries)}, names, nil, "more than one version"},
		"a change on no device": {failingJournal{entries: controllerSide(entries)}, names, nil, "on no device"},
	} {
		t.Run(name, func(t *testing.T) {
			err := rollback(api.WithRollback(tc.journal, tc.names))
			switch {
			case err == nil:
				t.Fatal("the rollback was planned")
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Errorf("error %v, want %v", err, tc.want)
			case tc.text != "" && !strings.Contains(fmt.Sprintf("%v %+v", err, refusedProblems(err)), tc.text):
				t.Errorf("error %v (%+v), want it to mention %q", err, refusedProblems(err), tc.text)
			}
		})
	}

	// Every change named to leave: nothing is left to run.
	all := api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[0]", "tasks[1]"}}
	if _, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, all); !errors.Is(err, api.ErrNotRollbackable) || !strings.Contains(err.Error(), "nothing is left") {
		t.Errorf("everything left in place: %v", err)
	}

	// The runbook it ran, gone.
	if err := os.Remove(filepath.Join(f.dir, "rb-1.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.dispatcher.Rollback(t.Context(), "g", f.jobID, req); !errors.Is(err, api.ErrNotRollbackable) || !strings.Contains(err.Error(), "cannot be read") {
		t.Errorf("the runbook gone: %v", err)
	}
}

// TestRollback_ARunWhoseTemplateCannotBeRepeatedIsRefused covers a job
// whose template is not wired, and one bound to a credential that asks
// for an input at launch, which a rollback cannot supply.
func TestRollback_ARunWhoseTemplateCannotBeRepeatedIsRefused(t *testing.T) {
	tmpl := launchableTemplate()
	tmpl.Definition = "rb-1"
	tmpl.CredentialIDs = []int{18}
	f := newRollbackFixture(t, &tmpl, nil)
	req := api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}}

	bare := api.NewDispatcher(f.source, f.jobs, f.bus, api.WithRollback(&f.journal, func(context.Context, []string) (map[string]string, error) { return nil, nil }))
	if _, err := bare.Rollback(t.Context(), "g", f.jobID, req); err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Errorf("no template reader: %v", err)
	}
	prompted := api.NewDispatcher(f.source, f.jobs, f.bus,
		api.WithRollback(&f.journal, func(context.Context, []string) (map[string]string, error) {
			return map[string]string{"dev-1": "web1"}, nil
		}),
		api.WithTemplates(stubTemplates{tmpl: tmpl}),
		api.WithCredentialReader(stubCredentialReader{
			bound: []credstore.Credential{{ID: 18, Name: "prod api", TypeID: 4}},
			types: map[int]credstore.CredentialType{4: promptingType()},
		}))
	if _, err := prompted.Rollback(t.Context(), "g", f.jobID, req); !errors.Is(err, api.ErrNotRollbackable) || !strings.Contains(err.Error(), "prod api") {
		t.Errorf("a credential that prompts at launch: %v", err)
	}
}

// TestRollbackJob_AnswersEachFailureWithItsStatus drives the handler's
// other answers.
func TestRollbackJob_AnswersEachFailureWithItsStatus(t *testing.T) {
	tmpl := launchableTemplate()
	tmpl.Definition = "rb-1"
	f := newRollbackFixture(t, &tmpl, nil)
	names := func(context.Context, []string) (map[string]string, error) {
		return map[string]string{"dev-1": "web1"}, nil
	}
	call := func(d *api.Dispatcher, id string, identity bool) int {
		req := httptest.NewRequest(http.MethodPost, "/jobs/"+id+"/rollback", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
		if identity {
			ctx = context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx)
		}
		rec := httptest.NewRecorder()
		d.RollbackJob(rec, req.WithContext(ctx))
		return rec.Code
	}
	for name, tc := range map[string]struct {
		d        *api.Dispatcher
		id       string
		identity bool
		want     int
	}{
		"no identity":   {f.dispatcher, f.jobID, false, http.StatusUnauthorized},
		"not a UUID":    {f.dispatcher, "job-1", true, http.StatusBadRequest},
		"no such job":   {f.dispatcher, "5f0e4ac2-0000-4000-8000-000000000000", true, http.StatusNotFound},
		"template gone": {api.NewDispatcher(f.source, f.jobs, f.bus, api.WithRollback(&f.journal, names), api.WithTemplates(stubTemplates{err: launch.ErrNotFound})), f.jobID, true, http.StatusUnprocessableEntity},
		"too much":      {api.NewDispatcher(f.source, f.jobs, f.bus, api.WithRollback(failingJournal{all: journal.ErrTooMuchToPlan}, names), api.WithTemplates(stubTemplates{tmpl: tmpl})), f.jobID, true, http.StatusUnprocessableEntity},
		"store down":    {api.NewDispatcher(f.source, f.jobs, f.bus, api.WithRollback(failingJournal{all: errors.New("down")}, names), api.WithTemplates(stubTemplates{tmpl: tmpl})), f.jobID, true, http.StatusInternalServerError},
	} {
		t.Run(name, func(t *testing.T) {
			if got := call(tc.d, tc.id, tc.identity); got != tc.want {
				t.Errorf("status %d, want %d", got, tc.want)
			}
		})
	}
}

// mixedVersions is entries with the second recorded at another version.
func mixedVersions(entries []engine.JournalEntry) []engine.JournalEntry {
	out := append([]engine.JournalEntry(nil), entries...)
	out[1].DAGVersion = "sha256:" + strings.Repeat("0", 64)
	return out
}

// controllerSide is entries with the first change made on no device.
func controllerSide(entries []engine.JournalEntry) []engine.JournalEntry {
	out := append([]engine.JournalEntry(nil), entries...)
	out[0].DeviceID = ""
	return out
}

// refusedProblems returns a refusal's problems, or nil.
func refusedProblems(err error) []api.RollbackProblem {
	var refused *api.RollbackRefusedError
	if errors.As(err, &refused) {
		return refused.Problems
	}
	return nil
}

// refusingBus is a bus that refuses every publish.
type refusingBus struct{ *capturingBus }

func (refusingBus) Publish(context.Context, string, event.Event) error {
	return errors.New("the broker is down")
}

// TestRollback_TheRestOfItsPaths covers a journal naming a node the
// runbook lacks, a rollback asked for as a check over HTTP, and a launch
// that cannot be published.
func TestRollback_TheRestOfItsPaths(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	names := func(context.Context, []string) (map[string]string, error) {
		return map[string]string{"dev-1": "web1"}, nil
	}
	entries := append([]engine.JournalEntry(nil), f.journal.entries[f.jobID]...)
	entries[1].NodeID = "tasks[9]"
	d := api.NewDispatcher(f.source, f.jobs, f.bus, api.WithRollback(failingJournal{entries: entries}, names))
	_, err := d.Rollback(t.Context(), "g", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute})
	if problems := refusedProblems(err); len(problems) == 0 || !strings.Contains(fmt.Sprint(problems), "has no node tasks[9]") {
		t.Errorf("a node the runbook lacks: %v %+v", err, problems)
	}

	down := api.NewDispatcher(f.source, f.jobs, refusingBus{f.bus}, api.WithRollback(&f.journal, names))
	if _, err := down.Rollback(t.Context(), "g", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}}); err == nil {
		t.Error("a rollback whose launch could not be published reported success")
	}

	req := httptest.NewRequest(http.MethodPost, "/jobs/"+f.jobID+"/rollback", strings.NewReader(`{"mode":"check","leave":["tasks[1]"]}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", f.jobID)
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	f.dispatcher.RollbackJob(rec, req)
	var accepted struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil || rec.Code != http.StatusAccepted {
		t.Fatalf("a checked rollback: %d %s", rec.Code, rec.Body)
	}
	if job, _, err := f.jobs.Get(t.Context(), accepted.JobID); err != nil || job.Fields[launch.ModeField] != "check" {
		t.Errorf("the checked rollback's job: %+v, %v", job, err)
	}
}

// TestLaunchFromTemplate_AnswersAWindowedPromptWith422 is the HTTP half of
// TestALaunchWithPromptedInputsCannotSetForks.
func TestLaunchFromTemplate_AnswersAWindowedPromptWith422(t *testing.T) {
	tmpl := launchableTemplate() // carries forks: 5
	tmpl.CredentialIDs = []int{18}
	d := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
	req := httptest.NewRequest(http.MethodPost, "/templates/12/launch", strings.NewReader(`{"credentials":{"18":{"api_token":"typed-at-launch"}}}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "12")
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	d.LaunchFromTemplate(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "cannot also set forks") || strings.Contains(rec.Body.String(), "typed-at-launch") {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
}

// refusingJobs is a job store that refuses to create a job.
type refusingJobs struct{ dispatch.JobStore }

func (refusingJobs) Create(context.Context, *dispatch.Job) error {
	return errors.New("the database is read-only")
}

// TestRollback_AJobThatCannotBeStoredIsNotPublished covers the rollback
// job's own write failing: the error is returned and nothing is launched.
func TestRollback_AJobThatCannotBeStoredIsNotPublished(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	names := func(context.Context, []string) (map[string]string, error) {
		return map[string]string{"dev-1": "web1"}, nil
	}
	d := api.NewDispatcher(f.source, refusingJobs{f.jobs}, f.bus, api.WithRollback(&f.journal, names))
	if _, err := d.Rollback(t.Context(), "g", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute, Leave: []string{"tasks[1]"}}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("Rollback = %v, want the store's refusal", err)
	}
	if f.bus.count() != 0 {
		t.Error("a rollback job that was never stored was launched")
	}
}
