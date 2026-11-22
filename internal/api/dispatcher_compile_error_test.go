// Tests for a template whose runbook does not compile (FAILURE_PATTERNS
// 1360): a launch, a check, a relaunch and a schedule's write are each refused
// with the builder's own message before a job exists, a schedule saved earlier
// records the refusal when it fires, and a runbook the source cannot find or
// read is still left to the fan-out, as it was.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// brokenRunbook parses and does not build: its handler takes every device's
// lock together, which the builder refuses naming the handler.
const brokenRunbook = `id: broken
tasks:
  - name: step
    noop: {}
    notify: restart
handlers:
  - name: restart
    lock_acquisition: all_at_plan_time
    noop: {}
`

// builderSays is text only the builder's own message for brokenRunbook holds.
var builderSays = []string{"does not compile", "restart", "lock_acquisition: all_at_plan_time"}

// compileSource is a real runbook directory and the source over it.
type compileSource struct {
	dir string
	src runbook.Source
}

// newCompileSource holds "broken", which does not build, and "plain", which
// does.
func newCompileSource(t *testing.T) *compileSource {
	t.Helper()
	c := &compileSource{dir: t.TempDir()}
	c.write(t, "broken", brokenRunbook)
	writeTestRunbook(t, c.dir, "plain", noopRunbookFQCN)
	src, err := runbook.NewDirSource(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	c.src = src
	return c
}

// write replaces runbook id with body, a minute later than the file it
// replaces, so the source compiles it again.
func (c *compileSource) write(t *testing.T, id, body string) {
	t.Helper()
	path := filepath.Join(c.dir, id+".yaml")
	later := time.Now()
	if info, err := os.Stat(path); err == nil {
		later = info.ModTime().Add(time.Minute)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// says reports each of wants that text lacks.
func says(text string, wants []string) []string {
	var missing []string
	for _, want := range wants {
		if !strings.Contains(text, want) {
			missing = append(missing, want)
		}
	}
	return missing
}

// TestLaunchTemplate_RefusesARunbookThatDoesNotCompile: a launch of a template
// whose runbook does not build is refused with the builder's message, leaving
// no job and publishing nothing, and so is a relaunch of a job the template
// ran while its runbook still built; the same template over a runbook that
// builds launches.
func TestLaunchTemplate_RefusesARunbookThatDoesNotCompile(t *testing.T) {
	c := newCompileSource(t)
	for _, tc := range []struct {
		definition string
		refused    bool
	}{{"broken", true}, {"plain", false}} {
		jobs := newTestJobStore(t)
		bus := newCapturingBus()
		tmpl := promptableTemplate()
		tmpl.Definition = tc.definition
		dispatcher := api.NewDispatcher(c.src, jobs, bus, api.WithTemplates(stubTemplates{tmpl: tmpl}))
		_, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
		if (err != nil) != tc.refused {
			t.Fatalf("%s: LaunchTemplate = %v, want refused %v", tc.definition, err, tc.refused)
		}
		_, published := bus.firstOnTopic(topology.JobRequestedSubject())
		listed, err2 := jobs.List(context.Background(), "", 10)
		if err2 != nil {
			t.Fatal(err2)
		}
		if tc.refused {
			var notCompiled *runbook.CompileError
			if !errors.As(err, &notCompiled) {
				t.Fatalf("the refusal %v is not a runbook.CompileError", err)
			}
			if missing := says(notCompiled.Message(), builderSays); len(missing) > 0 {
				t.Errorf("the refusal %q does not say %q", notCompiled.Message(), missing)
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
	before := api.NewDispatcher(c.src, jobs, newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
	jobID, _, err := before.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	tmpl.Definition = "broken"
	after := api.NewDispatcher(c.src, jobs, newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
	if _, _, err := after.Relaunch(context.Background(), "ada@example.com", jobID); err == nil {
		t.Error("Relaunch of a job whose runbook no longer builds was accepted")
	}
	if listed, _ := jobs.List(context.Background(), "", 10); len(listed) != 1 {
		t.Errorf("a refused relaunch left %d jobs, want the first one alone", len(listed))
	}
	// Over HTTP the relaunch is a 422 with the builder's message, never a 500.
	req := httptest.NewRequest(http.MethodPost, "/jobs/"+jobID+"/relaunch", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", jobID)
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	after.RelaunchJob(rec, req)
	if missing := says(rec.Body.String(), builderSays); rec.Code != http.StatusUnprocessableEntity || len(missing) > 0 {
		t.Errorf("relaunch over HTTP: status %d, body %s; want 422 saying %q", rec.Code, rec.Body.String(), builderSays)
	}
}

// TestLaunchRoutes_ARunbookThatDoesNotCompileIs422: over HTTP the launch route
// and the check route answer a 422 with the builder's message, never a 202 and
// a job that fails at fan-out.
func TestLaunchRoutes_ARunbookThatDoesNotCompileIs422(t *testing.T) {
	tmpl := promptableTemplate()
	tmpl.Definition = "broken"
	for name, serve := range map[string]func(d *api.Dispatcher, w http.ResponseWriter, r *http.Request){
		"launch": (*api.Dispatcher).LaunchFromTemplate,
		"check":  (*api.Dispatcher).CheckFromTemplate,
	} {
		t.Run(name, func(t *testing.T) {
			jobs := newTestJobStore(t)
			dispatcher := api.NewDispatcher(newCompileSource(t).src, jobs, newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
			req := httptest.NewRequest(http.MethodPost, "/templates/12/"+name, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "12")
			req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			serve(dispatcher, rec, req)
			if missing := says(rec.Body.String(), builderSays); rec.Code != http.StatusUnprocessableEntity || len(missing) > 0 {
				t.Errorf("status %d, body %s; want 422 saying %q", rec.Code, rec.Body.String(), builderSays)
			}
			if listed, _ := jobs.List(context.Background(), "", 10); len(listed) != 0 {
				t.Errorf("a refused %s left %d jobs", name, len(listed))
			}
		})
	}
}

// TestLaunchTemplate_ARunbookTheSourceCannotGiveIsStillLeftToFanOut: the
// refusal is for a runbook that does not compile and for nothing else. A
// runbook that is missing, or that the source cannot read, is not a
// runbook.CompileError, and its launch is accepted and left to the fan-out, as
// before.
func TestLaunchTemplate_ARunbookTheSourceCannotGiveIsStillLeftToFanOut(t *testing.T) {
	c := newCompileSource(t)
	writeTestRunbook(t, c.dir, "unreadable", noopRunbookFQCN)
	if err := os.Chmod(filepath.Join(c.dir, "unreadable.yaml"), 0o000); err != nil {
		t.Fatal(err)
	}
	for _, definition := range []string{"missing", "unreadable"} {
		t.Run(definition, func(t *testing.T) {
			_, err := c.src.Get(context.Background(), definition)
			if err == nil {
				// Root reads a file whatever its mode.
				t.Skipf("needs an unprivileged user: the source read %s", definition)
			}
			var notCompiled *runbook.CompileError
			if errors.As(err, &notCompiled) {
				t.Fatalf("%v is a runbook.CompileError, want a lookup or storage error", err)
			}
			jobs := newTestJobStore(t)
			bus := newCapturingBus()
			tmpl := promptableTemplate()
			tmpl.Definition = definition
			dispatcher := api.NewDispatcher(c.src, jobs, bus, api.WithTemplates(stubTemplates{tmpl: tmpl}))
			if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil); err != nil {
				t.Fatalf("LaunchTemplate = %v, want the launch left to the fan-out", err)
			}
			if listed, _ := jobs.List(context.Background(), "", 10); len(listed) != 1 {
				t.Errorf("%d jobs, want the one the fan-out will fail", len(listed))
			}
		})
	}
}

// TestSchedules_ARunbookThatDoesNotCompileIsRefusedAtTheWrite: a schedule of a
// template whose runbook does not build is refused when written, with the
// builder's message, and nothing is stored; one of a runbook that builds
// saves.
func TestSchedules_ARunbookThatDoesNotCompileIsRefusedAtTheWrite(t *testing.T) {
	who := operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute, auth.ScopeTemplateRead)
	c := newCompileSource(t)
	f := newLaunchableScheduleFixtureFor(t, who, c.src, "broken")
	status, body := f.post(t, scheduleBody("nightly", f.templateLaunchable, ""))
	if missing := says(body, builderSays); status != http.StatusBadRequest || len(missing) > 0 {
		t.Fatalf("status %d, body %s; want 400 saying %q", status, body, builderSays)
	}
	if n := f.client.Schedule.Query().CountX(context.Background()); n != 0 {
		t.Errorf("%d schedules were written despite the refusal", n)
	}
	plain := newLaunchableScheduleFixtureFor(t, who, c.src, "plain")
	if status, body := plain.post(t, scheduleBody("nightly", plain.templateLaunchable, "")); status != http.StatusCreated {
		t.Errorf("status %d for a schedule of a runbook that builds, want 201; body %s", status, body)
	}
}

// send makes one request of the fixture's router and returns the status and
// the body.
func (f *launchableScheduleFixture) send(t *testing.T, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// TestSchedules_OneSavedBeforeItsRunbookBrokeSkipsAndStaysEditable: a schedule
// saved while its runbook built, which fires after the runbook stopped
// building, records the occurrence as skipped with launch_failed, as every
// refusal a type makes is recorded, makes no job, and is not retried. The
// schedule can still be read, edited (renamed and disabled) and deleted, and
// only pointing a schedule at the broken runbook, or turning one back on, is
// refused.
func TestSchedules_OneSavedBeforeItsRunbookBrokeSkipsAndStaysEditable(t *testing.T) {
	ctx := context.Background()
	who := operator(auth.ScopeScheduleWrite, auth.ScopeScheduleRead, auth.ScopeRunbookExecute, auth.ScopeTemplateRead)
	c := newCompileSource(t)
	c.write(t, "later", "id: later\ntasks:\n  - name: step\n    noop: {}\n")
	f := newLaunchableScheduleFixtureFor(t, who, c.src, "later")

	status, body := f.post(t, scheduleBody("daily", f.templateLaunchable, ""))
	if status != http.StatusCreated {
		t.Fatalf("status %d writing a schedule of a runbook that builds; body %s", status, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == "" {
		t.Fatalf("no schedule id in %s (%v)", body, err)
	}

	// The runbook stops building after the schedule was saved.
	c.write(t, "later", strings.Replace(brokenRunbook, "id: broken", "id: later", 1))

	now := time.Now().UTC().Add(25 * time.Hour)
	scanner := schedule.NewScanner(f.schedules, f.launcher,
		schedule.WithClock(func() time.Time { return now }),
		schedule.WithLogger(slog.New(slog.NewJSONHandler(io.Discard, nil))))
	scanner.Sweep(ctx)
	scanner.Sweep(ctx)

	history, err := f.schedules.ListOccurrences(ctx, f.organizationID, created.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	refusedOccurrences := 0
	for _, o := range history {
		if o.Outcome == schedule.OutcomeFired {
			t.Errorf("occurrence %s fired job %q though its runbook does not build", o.OccurrenceAt, o.JobID)
		}
		if o.Outcome == schedule.OutcomeSkipped && o.Reason == schedule.ReasonLaunchFailed {
			refusedOccurrences++
		}
	}
	if refusedOccurrences != 1 {
		t.Errorf("%d of %d occurrences read skipped/%s after two sweeps, want the one refused and not retried",
			refusedOccurrences, len(history), schedule.ReasonLaunchFailed)
	}
	if listed, _ := f.jobs.List(ctx, "", 10); len(listed) != 0 {
		t.Errorf("the firing left %d jobs, want none", len(listed))
	}

	// Still readable and editable while the runbook does not build.
	path := "/api/v1/schedules/" + created.ID
	if status, body := f.send(t, http.MethodGet, path, ""); status != http.StatusOK {
		t.Errorf("GET = %d, want 200; body %s", status, body)
	}
	if status, body := f.send(t, http.MethodPatch, path, `{"name":"daily, renamed"}`); status != http.StatusOK {
		t.Errorf("renaming the schedule = %d, want 200; body %s", status, body)
	}
	if status, body := f.send(t, http.MethodPatch, path, `{"enabled":false}`); status != http.StatusOK {
		t.Errorf("disabling the schedule = %d, want 200; body %s", status, body)
	}
	// Turning it back on is a write that would fire, so it is refused.
	status, body = f.send(t, http.MethodPatch, path, `{"enabled":true}`)
	if missing := says(body, builderSays); status != http.StatusBadRequest || len(missing) > 0 {
		t.Errorf("enabling the schedule = %d, body %s; want 400 saying %q", status, body, builderSays)
	}
	// So is a new schedule of the same template.
	if status, body := f.post(t, scheduleBody("another", f.templateLaunchable, "")); status != http.StatusBadRequest {
		t.Errorf("a new schedule of the broken runbook = %d, want 400; body %s", status, body)
	}
	if status, body := f.send(t, http.MethodDelete, path, ""); status != http.StatusNoContent {
		t.Errorf("DELETE = %d, want 204; body %s", status, body)
	}
}

// editedBetweenReads is the real source with one runbook edited, once, after
// its first read: the window between a scheduled launch's own check and the
// launch itself.
type editedBetweenReads struct {
	runbook.Source
	edit func()
	done bool
}

// Get reads through the real source, then makes the edit after the first read.
func (e *editedBetweenReads) Get(ctx context.Context, id string) (*runbook.Runbook, error) {
	rb, err := e.Source.Get(ctx, id)
	if !e.done {
		e.done = true
		e.edit()
	}
	return rb, err
}

// TestDispatcherLaunch_ARunbookBrokenBetweenItsTwoReadsIsStillARefusal: a
// scheduled launch reads the runbook twice, once for what an unattended run
// refuses and once in the launch. A runbook that stops compiling between the
// two is the same refusal (a launchable.Refusal with the builder's message),
// so a scheduler records it and moves on, and no job is made.
func TestDispatcherLaunch_ARunbookBrokenBetweenItsTwoReadsIsStillARefusal(t *testing.T) {
	c := newCompileSource(t)
	src := &editedBetweenReads{Source: c.src, edit: func() {
		c.write(t, "plain", strings.Replace(brokenRunbook, "id: broken", "id: plain", 1))
	}}
	jobs := newTestJobStore(t)
	tmpl := promptableTemplate()
	tmpl.Definition = "plain"
	dispatcher := api.NewDispatcher(src, jobs, newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))

	_, err := dispatcher.Launch(context.Background(), launchable.Request{Actor: "scheduler:s-1", Standing: true})
	var refusal launchable.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Launch = %v, want a launchable.Refusal", err)
	}
	if missing := says(refusal.Message, builderSays); len(missing) > 0 || refusal.Field != launchable.TargetField {
		t.Errorf("the refusal %q on field %q does not say %q on the target", refusal.Message, refusal.Field, missing)
	}
	if listed, _ := jobs.List(context.Background(), "", 10); len(listed) != 0 {
		t.Errorf("the refused launch left %d jobs", len(listed))
	}

	// Standing is a Preflight's to read: a launch of a runbook that is already
	// broken is refused whatever the request says.
	if _, err := dispatcher.Launch(context.Background(), launchable.Request{Actor: "scheduler:s-1", Standing: true}); !errors.As(err, &refusal) {
		t.Errorf("Launch with Standing set = %v, want the refusal", err)
	}
	// And a Preflight told the write is standing lets it through, while one
	// that is not is refused.
	if err := dispatcher.Preflight(context.Background(), launchable.Request{Standing: true}); err != nil {
		t.Errorf("Preflight of a standing write = %v, want nil", err)
	}
	if err := dispatcher.Preflight(context.Background(), launchable.Request{}); !errors.As(err, &refusal) {
		t.Errorf("Preflight of a new write = %v, want the refusal", err)
	}
}
