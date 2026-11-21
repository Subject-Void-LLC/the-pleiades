// Tests that every launch of a template whose runbook's condition reads a
// stored fact is refused before a job exists, and a schedule of one at its
// write, by the one dispatch.RefuseFactReads, since no Runner can read a
// stored fact in this release; and that a rollback of such a runbook's job is
// not refused for it.
package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// factsReadingRunbook decides on a stored fact in its second task.
const factsReadingRunbook = "id: reads\ntasks:\n  - name: step\n    noop: {}\n  - name: on debian\n    when: facts[\"os.family\"] == \"debian\"\n    noop: {}\n"

// factReadsSource is a runbook source holding "reads" and "plain", which reads
// no fact.
func factReadsSource(t *testing.T) runbook.Source {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "reads.yaml"), []byte(factsReadingRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRunbook(t, dir, "plain", noopRunbookFQCN)
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestLaunchTemplate_RefusesAFactReadingRunbookBeforeTheJobExists: a launch,
// and a relaunch of a job the template ran before its runbook read facts,
// are refused with ErrFactReads naming the task and the key, leaving no job
// and publishing nothing; the same template naming a runbook that reads no
// fact launches.
func TestLaunchTemplate_RefusesAFactReadingRunbookBeforeTheJobExists(t *testing.T) {
	src := factReadsSource(t)
	for _, tc := range []struct {
		definition string
		refused    bool
	}{{"reads", true}, {"plain", false}} {
		jobs := newTestJobStore(t)
		bus := newCapturingBus()
		tmpl := promptableTemplate()
		tmpl.Definition = tc.definition
		dispatcher := api.NewDispatcher(src, jobs, bus, api.WithTemplates(stubTemplates{tmpl: tmpl}))
		_, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
		if got := errors.Is(err, dispatch.ErrFactReads); got != tc.refused {
			t.Fatalf("%s: LaunchTemplate = %v, refused %v, want %v", tc.definition, err, got, tc.refused)
		}
		_, published := bus.firstOnTopic(topology.JobRequestedSubject())
		listed, err2 := jobs.List(context.Background(), "", 10)
		if err2 != nil {
			t.Fatal(err2)
		}
		if tc.refused {
			for _, want := range []string{`tasks[1] (name "on debian")`, `facts["os.family"]`, "Runner cannot read stored facts"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal %q does not say %q", err, want)
				}
			}
			if published || len(listed) != 0 {
				t.Errorf("a refused launch published %v and left %d jobs, want neither", published, len(listed))
			}
		} else if !published || len(listed) != 1 {
			t.Errorf("control: published %v, %d jobs; want one of each", published, len(listed))
		}
	}

	// A relaunch reads the runbook as it is now.
	jobs := newTestJobStore(t)
	tmpl := promptableTemplate()
	tmpl.Definition = "plain"
	before := api.NewDispatcher(src, jobs, newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
	jobID, _, err := before.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.Definition = "reads"
	after := api.NewDispatcher(src, jobs, newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
	if _, _, err := after.Relaunch(context.Background(), "ada@example.com", jobID); !errors.Is(err, dispatch.ErrFactReads) {
		t.Errorf("Relaunch = %v, want ErrFactReads", err)
	}
	if listed, _ := jobs.List(context.Background(), "", 10); len(listed) != 1 {
		t.Errorf("a refused relaunch left %d jobs, want the first one alone", len(listed))
	}
}

// TestLaunchRoutes_AFactReadingRunbookIs422: over HTTP the launch route and
// the check route answer a 422 naming the task and the key, never a 202 and
// a job failing every device.
func TestLaunchRoutes_AFactReadingRunbookIs422(t *testing.T) {
	tmpl := promptableTemplate()
	tmpl.Definition = "reads"
	for name, serve := range map[string]func(d *api.Dispatcher, w http.ResponseWriter, r *http.Request){
		"launch": (*api.Dispatcher).LaunchFromTemplate,
		"check":  (*api.Dispatcher).CheckFromTemplate,
	} {
		t.Run(name, func(t *testing.T) {
			jobs := newTestJobStore(t)
			dispatcher := api.NewDispatcher(factReadsSource(t), jobs, newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
			req := httptest.NewRequest(http.MethodPost, "/templates/12/"+name, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "12")
			req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			serve(dispatcher, rec, req)
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `os.family`) {
				t.Errorf("status %d, body %s; want 422 naming the key", rec.Code, rec.Body.String())
			}
			if listed, _ := jobs.List(context.Background(), "", 10); len(listed) != 0 {
				t.Errorf("a refused %s left %d jobs", name, len(listed))
			}
		})
	}
}

// TestSchedules_AFactReadingRunbookIsRefusedAtTheWrite: a schedule of a
// template whose runbook reads a stored fact is refused when written, naming
// the task and the key, and nothing is stored; one of a runbook reading none
// saves.
func TestSchedules_AFactReadingRunbookIsRefusedAtTheWrite(t *testing.T) {
	who := operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute, auth.ScopeTemplateRead)
	src := factReadsSource(t)
	f := newLaunchableScheduleFixtureFor(t, who, src, "reads")
	status, body := f.post(t, scheduleBody("nightly", f.templateLaunchable, ""))
	if status != http.StatusBadRequest || !strings.Contains(body, "os.family") || !strings.Contains(body, "Runner cannot read stored facts") {
		t.Fatalf("status %d, body %s; want 400 naming the key", status, body)
	}
	if n := f.client.Schedule.Query().CountX(context.Background()); n != 0 {
		t.Errorf("%d schedules were written despite the refusal", n)
	}
	plain := newLaunchableScheduleFixtureFor(t, who, src, "plain")
	if status, body := plain.post(t, scheduleBody("nightly", plain.templateLaunchable, "")); status != http.StatusCreated {
		t.Errorf("status %d for a schedule of a runbook reading no fact, want 201; body %s", status, body)
	}
}

// TestRollback_AJobOfAFactReadingRunbookIsNotRefusedForIt: a rollback runs its
// plan's undo steps, which read no fact, so undoing a job whose runbook's
// later task reads one launches.
func TestRollback_AJobOfAFactReadingRunbookIsNotRefusedForIt(t *testing.T) {
	source := `id: rb-1
tasks:
  - name: make a directory
    file.directory:
      path: /tmp/made
  - name: on debian
    when: facts["os.family"] == "debian"
    file.directory:
      path: /tmp/debian
`
	f := newRollbackFixtureOf(t, nil, nil, source, func(entry func(node, fqcn string, seq int) engine.JournalEntry) []engine.JournalEntry {
		made := entry("tasks[0]", "file.directory", 1)
		made.InverseFQCN, made.InverseParamKeys, made.InverseComplete = "file.remove", []string{"path"}, true
		made.InverseParams = []engine.InverseParam{{Key: "path", Text: text("/tmp/made")}}
		return []engine.JournalEntry{made}
	})
	if _, err := f.dispatcher.Rollback(t.Context(), "grace@example.com", f.jobID, api.RollbackRequest{Mode: collection.ModeExecute}); err != nil {
		t.Fatalf("Rollback = %v, want the undo launched", err)
	}
	if f.bus.count() != 1 {
		t.Errorf("published %d launches, want the rollback's one", f.bus.count())
	}
}
