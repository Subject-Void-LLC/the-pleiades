package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers what the Template surface refuses, which is most of its
// surface area by line count and all of it by consequence. Every case here
// has an alternative that is silently worse than an error: a dropped
// narrowing answers a different question than the one asked, a swallowed
// store failure reads as an empty collection, and an unauthenticated write
// records the empty string as an author.

// failingTemplateStore refuses everything with an error that is not one of
// the domain sentinels.
//
// A double, and the narrow kind this project allows: the behaviour under
// test is that an unclassifiable store failure becomes a 500 with the
// store's own text logged rather than served, and a real store cannot be
// made to fail on demand without also breaking the assertion. Every other
// test in templates_test.go runs against the real ent-backed store.
type failingTemplateStore struct{ err error }

func (s failingTemplateStore) Create(context.Context, launch.Template) (launch.Template, error) {
	return launch.Template{}, s.err
}
func (s failingTemplateStore) Get(context.Context, int) (launch.Template, error) {
	return launch.Template{}, s.err
}
func (s failingTemplateStore) List(context.Context, launch.Query) ([]launch.Template, error) {
	return nil, s.err
}
func (s failingTemplateStore) Update(context.Context, launch.Template) error { return s.err }
func (s failingTemplateStore) Delete(context.Context, int) error             { return s.err }
func (s failingTemplateStore) SavedConfigs(context.Context, int) ([]launch.SavedConfig, error) {
	return nil, s.err
}
func (s failingTemplateStore) SaveConfig(context.Context, launch.SavedConfig) (launch.SavedConfig, error) {
	return launch.SavedConfig{}, s.err
}

// failingRouter mounts the administration endpoints over a store that
// refuses.
func failingRouter(t *testing.T, err error) http.Handler {
	t.Helper()
	handler := api.NewTemplateHandler(failingTemplateStore{err: err}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, buildErr := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListTemplates.Route(handler.List),
			apispec.GetTemplate.Route(handler.Get),
			apispec.CreateTemplate.Route(handler.Create),
			apispec.UpdateTemplate.Route(handler.Update),
			apispec.DeleteTemplate.Route(handler.Delete),
			apispec.CopyTemplate.Route(handler.Copy),
			apispec.ListTemplateConfigs.Route(handler.ListConfigs),
			apispec.CreateTemplateConfig.Route(handler.CreateConfig),
		},
	})
	if buildErr != nil {
		t.Fatalf("NewRouter: %v", buildErr)
	}
	return router
}

func TestTemplateAPI_RefusesAMalformedRequestRatherThanNarrowingItSilently(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))
	id := strconv.Itoa(tmpl.ID)

	cases := map[string]struct {
		method, target, body string
		wantStatus           int
	}{
		// A malformed narrowing is refused rather than dropped: silently
		// ignoring it answers a different question than the one asked, in a
		// shape nobody rechecks.
		"a limit that is not a number":    {http.MethodGet, "/api/v1/templates?limit=all", "", http.StatusBadRequest},
		"a negative cursor":               {http.MethodGet, "/api/v1/templates?after=-3", "", http.StatusBadRequest},
		"an organization that is not one": {http.MethodGet, "/api/v1/templates?organization=0", "", http.StatusBadRequest},

		// An id that reaches a primary-key lookup is constrained at the
		// boundary rather than trusted to every layer beneath it.
		"a zero id":                    {http.MethodGet, "/api/v1/templates/0", "", http.StatusBadRequest},
		"an id that is not a number":   {http.MethodGet, "/api/v1/templates/seven", "", http.StatusBadRequest},
		"a zero id on launch":          {http.MethodPost, "/api/v1/templates/0/launch", "", http.StatusBadRequest},
		"a zero id on configs":         {http.MethodGet, "/api/v1/templates/0/configs", "", http.StatusBadRequest},
		"a zero id on update":          {http.MethodPatch, "/api/v1/templates/0", `{"name":"x"}`, http.StatusBadRequest},
		"a zero id on delete":          {http.MethodDelete, "/api/v1/templates/0", "", http.StatusBadRequest},
		"a zero id on copy":            {http.MethodPost, "/api/v1/templates/0/copy", "", http.StatusBadRequest},
		"a zero id on saving a config": {http.MethodPost, "/api/v1/templates/0/configs", `{"name":"x"}`, http.StatusBadRequest},
		"a malformed copy":             {http.MethodPost, "/api/v1/templates/" + id + "/copy", "{not json", http.StatusBadRequest},

		// A body this API cannot decode is refused with its category only:
		// echoing the caller's own bytes back is the reflection shape
		// FAILURE_PATTERNS.md #72 records.
		"a malformed create":        {http.MethodPost, "/api/v1/templates", "{not json", http.StatusBadRequest},
		"a malformed update":        {http.MethodPatch, "/api/v1/templates/" + id, "{not json", http.StatusBadRequest},
		"a malformed launch":        {http.MethodPost, "/api/v1/templates/" + id + "/launch", "{not json", http.StatusBadRequest},
		"a malformed configuration": {http.MethodPost, "/api/v1/templates/" + id + "/configs", "{not json", http.StatusBadRequest},
		"a field no endpoint reads": {http.MethodPost, "/api/v1/templates", `{"name":"x","surprise":1}`, http.StatusBadRequest},

		// A saved configuration nobody can pick out of a list is not one
		// somebody saved on purpose.
		"a configuration with no name": {http.MethodPost, "/api/v1/templates/" + id + "/configs", `{"fields":{"limit":"a"}}`, http.StatusBadRequest},

		// Records that are not there, on every path that takes an id.
		"reading a template that is gone":          {http.MethodGet, "/api/v1/templates/4242", "", http.StatusNotFound},
		"updating a template that is gone":         {http.MethodPatch, "/api/v1/templates/4242", `{"name":"x"}`, http.StatusNotFound},
		"deleting a template that is gone":         {http.MethodDelete, "/api/v1/templates/4242", "", http.StatusNotFound},
		"copying a template that is gone":          {http.MethodPost, "/api/v1/templates/4242/copy", "", http.StatusNotFound},
		"listing configs of one that is gone":      {http.MethodGet, "/api/v1/templates/4242/configs", "", http.StatusNotFound},
		"saving a config against one that is gone": {http.MethodPost, "/api/v1/templates/4242/configs", `{"name":"x"}`, http.StatusNotFound},
		"launching one that is gone":               {http.MethodPost, "/api/v1/templates/4242/launch", "", http.StatusNotFound},
		"launching from a config that is gone":     {http.MethodPost, "/api/v1/templates/" + id + "/launch", `{"config": 4242}`, http.StatusNotFound},

		// A relaunch of an id that is not a job id at all.
		"a relaunch of something that is not a uuid": {http.MethodPost, "/api/v1/jobs/not-a-uuid/relaunch", "", http.StatusBadRequest},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, f.router, tc.method, tc.target, tc.body)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// TestTemplateAPI_RefusesASavedModeTheTemplateCouldNeverRun covers the
// run mode half of saving a configuration: a value that is not a mode, and
// a real run saved beneath a template that makes every launch a check, are
// refused when saved, with the rule's own reason, rather than failing every
// time a schedule fires from them. The control is a check saved against a
// template with no mode, which any layer may ask for.
func TestTemplateAPI_RefusesASavedModeTheTemplateCouldNeverRun(t *testing.T) {
	f := newTemplateFixture(t)
	plain := f.createTemplate(t, fmt.Sprintf(`{"name": "plain", "kind": "runbook", "definition": "patch-edge", "inventory": %d}`, f.invA))
	checked := f.createTemplate(t, fmt.Sprintf(`{"name": "drift", "kind": "runbook", "definition": "patch-edge", "inventory": %d, "defaults": {"mode": "check"}}`, f.invA))

	for _, tc := range []struct {
		name     string
		template int
		body     string
		status   int
		reason   string
	}{
		{"not a mode", plain.ID, `{"name": "bad", "fields": {"mode": "rehearse"}}`, http.StatusUnprocessableEntity, "asks for mode rehearse"},
		{"a real run beneath a check", checked.ID, `{"name": "bad", "fields": {"mode": "execute"}}`, http.StatusUnprocessableEntity, "never turned back into a real run"},
		{"a check", plain.ID, `{"name": "drift", "fields": {"mode": "check"}}`, http.StatusCreated, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, f.router, http.MethodPost, configsPath(tc.template), tc.body)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.reason) {
				t.Errorf("status = %d, body %s; want %d naming %q", rec.Code, rec.Body.String(), tc.status, tc.reason)
			}
		})
	}
}

func TestTemplateAPI_RefusesAnAnswerASavedConfigurationCouldNotSurvive(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, fmt.Sprintf(`{
		"name": "upgrade", "kind": "runbook", "definition": "patch-edge", "inventory": %d,
		"survey": {"enabled": true, "questions": [
			{"variable": "version", "label": "VERSION", "type": "multiplechoice", "required": true, "choices": ["17.3"]},
			{"variable": "region", "label": "REGION", "type": "text"}
		]}
	}`, f.invA))

	// An answer outside the offered choices is refused at the save, not at
	// the launch. A configuration that fails the moment somebody launches
	// from it fails at the worst possible moment.
	rec := doJSON(t, f.router, http.MethodPost, configsPath(tmpl.ID),
		`{"name": "bad", "answers": {"version": "99.9"}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	// But a configuration that answers nothing required is stored: it is
	// legitimately partial, since the required answer may arrive at launch,
	// and refusing it would demand an answer to a question nobody is asking
	// yet.
	rec = doJSON(t, f.router, http.MethodPost, configsPath(tmpl.ID),
		`{"name": "partial", "fields": {"limit": "edge-*"}, "answers": {"region": "eu-west"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a partial configuration: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var saved decodedConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	// Launching from it still needs the required answer, which is where the
	// two halves meet: the configuration carries the routine values and the
	// operator supplies the one the author insisted on.
	if rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID),
		fmt.Sprintf(`{"config": %d}`, saved.ID)); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("launching from a partial configuration with no answer: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID),
		fmt.Sprintf(`{"config": %d, "answers": {"version": "17.3"}}`, saved.ID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launching with the missing answer supplied: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}

	// And the stored answer travelled with it: the launch's own answers
	// layer over the configuration's rather than replacing them.
	var launched decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	ctx := context.Background()
	job, _, err := f.jobs.Get(ctx, launched.JobID)
	if err != nil {
		t.Fatalf("reading the launched job: %v", err)
	}
	recorded, err := f.store.GetConfig(ctx, job.LaunchConfigID)
	if err != nil {
		t.Fatalf("reading the recorded configuration: %v", err)
	}
	if recorded.Answers["region"] != "eu-west" || recorded.Answers["version"] != "17.3" {
		t.Errorf("the launch ran with answers %v, want the configuration's region and the launch's version", recorded.Answers)
	}
}

func TestTemplateAPI_ReportsAStoreFailureWithoutServingItsText(t *testing.T) {
	// The store's own error text can name a constraint, a column or a file
	// path, which a caller holding only template:read has no business
	// reading. Only the category crosses the boundary.
	const internal = "pq: relation \"templates\" does not exist"
	router := failingRouter(t, errors.New(internal))

	for name, target := range map[string]struct {
		method, path, body string
	}{
		"list":    {http.MethodGet, "/api/v1/templates", ""},
		"get":     {http.MethodGet, "/api/v1/templates/1", ""},
		"create":  {http.MethodPost, "/api/v1/templates", `{"name":"x","kind":"runbook","definition":"y","inventory":1}`},
		"update":  {http.MethodPatch, "/api/v1/templates/1", `{"name":"x"}`},
		"delete":  {http.MethodDelete, "/api/v1/templates/1", ""},
		"copy":    {http.MethodPost, "/api/v1/templates/1/copy", ""},
		"configs": {http.MethodGet, "/api/v1/templates/1/configs", ""},
		"save":    {http.MethodPost, "/api/v1/templates/1/configs", `{"name":"x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, router, target.method, target.path, target.body)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), internal) {
				t.Errorf("the response served the store's own error text: %s", rec.Body.String())
			}
		})
	}
}

// TestTemplateAPI_FailsClosedWithoutAnIdentity calls the launch handlers
// directly rather than through a router, for the reason inventories.go's
// own identical test records: api.RequireScope answers 401 before any
// handler runs, so mounted on its declared route this branch is
// unreachable. It exists as defence in depth for a handler mounted outside
// that middleware, where the alternative is dispatching a job with the
// empty string as its actor.
func TestTemplateAPI_FailsClosedWithoutAnIdentity(t *testing.T) {
	f := newTemplateFixture(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus(),
		api.WithTemplates(f.store), api.WithLaunchConfigs(f.store))

	for name, handler := range map[string]http.HandlerFunc{
		"launch":   dispatcher.LaunchFromTemplate,
		"relaunch": dispatcher.RelaunchJob,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/templates/1/launch", strings.NewReader(""))
			handler(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRelaunch_ReportsAConfigurationThatIsGoneRatherThanLaunchingWithout(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), `{"overrides": {"limit": "edge-07"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launching: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var launched decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	// The configuration is deleted out from under the job while the
	// template it belongs to lives on. Launching anyway would run something
	// other than the job being repeated, so this reports rather than
	// silently downgrading to a plain launch.
	ctx := context.Background()
	job, _, err := f.jobs.Get(ctx, launched.JobID)
	if err != nil {
		t.Fatalf("reading the launched job: %v", err)
	}
	if err := f.client.SavedLaunchConfig.DeleteOneID(job.LaunchConfigID).Exec(ctx); err != nil {
		t.Fatalf("deleting the configuration: %v", err)
	}

	rec = doJSON(t, f.router, http.MethodPost, "/api/v1/jobs/"+launched.JobID+"/relaunch", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("relaunching a job whose configuration is gone: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	// Deleting the whole template is the other half of the same shape, and
	// it reports the template rather than the configuration: the caller
	// asked for a job that exists, so a 404 would name the wrong resource.
	if err := f.store.Delete(ctx, tmpl.ID); err != nil {
		t.Fatalf("deleting the template: %v", err)
	}
	rec = doJSON(t, f.router, http.MethodPost, "/api/v1/jobs/"+launched.JobID+"/relaunch", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("relaunching a job whose template is gone: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "template") {
		t.Errorf("the refusal says %q, want it to name the template", rec.Body.String())
	}
}

func TestRelaunch_RepeatsAJobThatSuppliedNothing(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	// A launch that overrode nothing records no configuration, because
	// there is nothing to record. Relaunching it is not a failure: the
	// template's own defaults are exactly what ran.
	rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launching: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var launched decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	job, _, err := f.jobs.Get(context.Background(), launched.JobID)
	if err != nil {
		t.Fatalf("reading the launched job: %v", err)
	}
	if job.LaunchConfigID != 0 {
		t.Errorf("a launch that supplied nothing recorded configuration %d", job.LaunchConfigID)
	}

	if rec := doJSON(t, f.router, http.MethodPost,
		"/api/v1/jobs/"+launched.JobID+"/relaunch", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("relaunching it: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

func TestLaunch_ReportsAKindThisControllerCannotRun(t *testing.T) {
	f := newTemplateFixture(t)

	// A template stored under a kind nothing registers. It is reachable
	// through the store rather than the API because the API refuses to
	// create one, which is the point: this is the deployment that dropped a
	// kind after templates were already saved against it, not a caller's
	// mistake, so the answer names the controller rather than the request.
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))
	if err := f.client.Template.UpdateOneID(tmpl.ID).SetKind("terraform").Exec(context.Background()); err != nil {
		t.Fatalf("re-pointing the stored kind: %v", err)
	}

	rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	// And it reads as a template with no runnable kind rather than as a
	// template that does not exist.
	if rec := doJSON(t, f.router, http.MethodGet, "/api/v1/templates/"+strconv.Itoa(tmpl.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("reading it: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// faultyStore wraps the real ent-backed store and fails one chosen method.
//
// It exists for the branches an always-failing store cannot reach: every
// handler that touches storage twice reads the template first, so a store
// that refuses everything stops at the read and the second call's failure
// path never runs. Those paths are the ones that matter operationally --
// the row deleted between an update and its read-back, the survey write
// that fails after the template row was already changed -- so they are
// proved rather than assumed.
type faultyStore struct {
	launch.Store

	failGetAfter int // Get succeeds this many times, then fails.
	gets         int

	failUpdate       error
	failSavedConfigs error
	failSaveConfig   error
	failDelete       error
	failCreate       error
	failGetConfig    error
}

func (s *faultyStore) GetConfig(ctx context.Context, id int) (launch.SavedConfig, error) {
	if s.failGetConfig != nil {
		return launch.SavedConfig{}, s.failGetConfig
	}
	return s.Store.GetConfig(ctx, id)
}

func (s *faultyStore) Get(ctx context.Context, id int) (launch.Template, error) {
	s.gets++
	if s.failGetAfter > 0 && s.gets > s.failGetAfter {
		return launch.Template{}, errors.New("the row vanished between two reads")
	}
	return s.Store.Get(ctx, id)
}

func (s *faultyStore) Create(ctx context.Context, tmpl launch.Template) (launch.Template, error) {
	if s.failCreate != nil {
		return launch.Template{}, s.failCreate
	}
	return s.Store.Create(ctx, tmpl)
}

func (s *faultyStore) Update(ctx context.Context, tmpl launch.Template) error {
	if s.failUpdate != nil {
		return s.failUpdate
	}
	return s.Store.Update(ctx, tmpl)
}

func (s *faultyStore) Delete(ctx context.Context, id int) error {
	if s.failDelete != nil {
		return s.failDelete
	}
	return s.Store.Delete(ctx, id)
}

func (s *faultyStore) SavedConfigs(ctx context.Context, templateID int) ([]launch.SavedConfig, error) {
	if s.failSavedConfigs != nil {
		return nil, s.failSavedConfigs
	}
	return s.Store.SavedConfigs(ctx, templateID)
}

func (s *faultyStore) SaveConfig(ctx context.Context, cfg launch.SavedConfig) (launch.SavedConfig, error) {
	if s.failSaveConfig != nil {
		return launch.SavedConfig{}, s.failSaveConfig
	}
	return s.Store.SaveConfig(ctx, cfg)
}

func TestTemplateAPI_ReportsAFailureAfterTheFirstReadSucceeded(t *testing.T) {
	boom := errors.New("the database went away")

	cases := map[string]struct {
		faults         func(*faultyStore)
		method, suffix string
		body           string
	}{
		"the update itself fails":             {func(s *faultyStore) { s.failUpdate = boom }, http.MethodPatch, "", `{"name":"renamed"}`},
		"the read-back after an update fails": {func(s *faultyStore) { s.failGetAfter = 1 }, http.MethodPatch, "", `{"name":"renamed"}`},
		"the delete fails":                    {func(s *faultyStore) { s.failDelete = boom }, http.MethodDelete, "", ""},
		"the copy's create fails":             {func(s *faultyStore) { s.failCreate = boom }, http.MethodPost, "/copy", ""},
		"listing the configurations fails":    {func(s *faultyStore) { s.failSavedConfigs = boom }, http.MethodGet, "/configs", ""},
		"storing a configuration fails":       {func(s *faultyStore) { s.failSaveConfig = boom }, http.MethodPost, "/configs", `{"name":"eu"}`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newTemplateFixture(t)
			tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

			faulty := &faultyStore{Store: f.store}
			tc.faults(faulty)

			handler := api.NewTemplateHandler(faulty, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			router, err := api.NewRouter(api.RouterConfig{
				Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
				Auth:      alwaysAuthenticated,
				Admission: &fakeAdmitter{},
				HATEOAS:   allowAllGenerator(t),
				Routes: []api.Route{
					apispec.UpdateTemplate.Route(handler.Update),
					apispec.DeleteTemplate.Route(handler.Delete),
					apispec.CopyTemplate.Route(handler.Copy),
					apispec.ListTemplateConfigs.Route(handler.ListConfigs),
					apispec.CreateTemplateConfig.Route(handler.CreateConfig),
				},
			})
			if err != nil {
				t.Fatalf("NewRouter: %v", err)
			}

			rec := doJSON(t, router, tc.method, "/api/v1/templates/"+strconv.Itoa(tmpl.ID)+tc.suffix, tc.body)
			if rec.Code != http.StatusInternalServerError {
				t.Errorf("status = %d, want 500: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLaunch_ReportsAJobItCouldNotPersist(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	// The template resolves, the configuration records, and then the job
	// will not save. A caller must not be told 202 for a job that does not
	// exist: it would poll it and get a 404 for a dispatch it was told had
	// been accepted.
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"),
		&erroringJobStore{err: errors.New("the database is unreachable")}, newCapturingBus(),
		api.WithTemplates(f.store), api.WithLaunchConfigs(f.store))

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.LaunchTemplate.Route(dispatcher.LaunchFromTemplate)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	rec := doJSON(t, router, http.MethodPost, launchPath(tmpl.ID), "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "unreachable") {
		t.Error("the response served the store's own error text")
	}
}

func TestRelaunch_RefusesWhenTheConfigurationPortIsNotWired(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	// A Dispatcher that can read templates but not configurations. The job
	// below names one, so repeating it is impossible; launching without it
	// would run something other than the job being repeated.
	full := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus(),
		api.WithTemplates(f.store), api.WithLaunchConfigs(f.store))
	jobID, _, err := full.LaunchTemplate(context.Background(), "ada@example.com", tmpl.ID,
		launch.Config{Overrides: launch.Fields{"limit": "edge-07"}}, nil)
	if err != nil {
		t.Fatalf("LaunchTemplate: %v", err)
	}

	partial := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus(),
		api.WithTemplates(f.store))
	if _, _, err := partial.Relaunch(context.Background(), "ada@example.com", jobID); err == nil {
		t.Error("Relaunch repeated a job whose configuration it could not read")
	}

	// And one with no templates port at all refuses before it reads
	// anything, rather than falling back to a launch that names no template.
	bare := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus())
	if _, _, err := bare.Relaunch(context.Background(), "ada@example.com", jobID); err == nil {
		t.Error("Relaunch succeeded with no template port wired")
	}
}

func TestNewTemplateHandler_DefaultsItsLogger(t *testing.T) {
	// A nil logger is a caller's omission, not a reason to panic on the
	// first store failure. Every handler in this package takes the same
	// posture.
	if api.NewTemplateHandler(failingTemplateStore{err: errors.New("x")}, nil) == nil {
		t.Fatal("NewTemplateHandler returned nil")
	}
}

func TestTemplateAPI_MapsEveryStoreRefusalOntoTheStatusItDeserves(t *testing.T) {
	// The mapping is asserted at the handler rather than only through the
	// store, because two of these cannot be provoked through this API
	// today: the organization is derived from the inventory rather than
	// submitted, so the store's cross-tenant refusal has no way to fire
	// from here. The mapping still has to be right, since the alternative
	// when some later path does submit one is a 500 for what is a caller's
	// mistake.
	for name, tc := range map[string]struct {
		err        error
		wantStatus int
	}{
		"a name already taken":         {launch.ErrExists, http.StatusConflict},
		"another tenant's inventory":   {launch.ErrCrossTenant, http.StatusForbidden},
		"a template that is not there": {launch.ErrNotFound, http.StatusNotFound},
		"a survey nobody could answer": {launch.ErrInvalidSurvey, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			router := failingRouter(t, tc.err)
			rec := doJSON(t, router, http.MethodPost, "/api/v1/templates",
				`{"name":"x","kind":"runbook","definition":"y","inventory":1}`)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestTemplateAPI_CarriesAPageRequestToTheStore(t *testing.T) {
	f := newTemplateFixture(t)
	first := f.createTemplate(t, f.gateTemplateBody("alpha"))
	f.createTemplate(t, fmt.Sprintf(`{"name":"beta","kind":"runbook","definition":"other","inventory":%d}`, f.invA))

	// A cursor and a bound, both applied rather than accepted and ignored.
	rec := doJSON(t, f.router, http.MethodGet,
		fmt.Sprintf("/api/v1/templates?after=%d&limit=1", first.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Templates []decodedTemplate `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(listed.Templates) != 1 || listed.Templates[0].Name != "beta" {
		t.Errorf("paging past %d returned %+v, want the one after it", first.ID, listed.Templates)
	}

	// And a name filter.
	rec = doJSON(t, f.router, http.MethodGet, "/api/v1/templates?q=alph", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(listed.Templates) != 1 || listed.Templates[0].Name != "alpha" {
		t.Errorf("filtering by name returned %+v", listed.Templates)
	}
}

func TestLaunch_ReportsAConfigurationItCouldNotReadOrRecord(t *testing.T) {
	boom := errors.New("the database went away")

	t.Run("recording what a launch supplied fails", func(t *testing.T) {
		f := newTemplateFixture(t)
		tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

		router := launchRouter(t, f, &faultyStore{Store: f.store, failSaveConfig: boom})
		rec := doJSON(t, router, http.MethodPost, launchPath(tmpl.ID), `{"overrides":{"limit":"edge-01"}}`)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("reading a saved configuration fails", func(t *testing.T) {
		f := newTemplateFixture(t)
		tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

		rec := doJSON(t, f.router, http.MethodPost, configsPath(tmpl.ID), `{"name":"eu","fields":{"limit":"eu-*"}}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("saving a configuration: status = %d: %s", rec.Code, rec.Body.String())
		}
		var saved decodedConfig
		if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
			t.Fatalf("decoding: %v", err)
		}

		router := launchRouter(t, f, &faultyStore{Store: f.store, failGetConfig: boom})
		rec = doJSON(t, router, http.MethodPost, launchPath(tmpl.ID), fmt.Sprintf(`{"config":%d}`, saved.ID))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("a job cannot be read at all", func(t *testing.T) {
		f := newTemplateFixture(t)
		dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"),
			&erroringJobStore{err: boom}, newCapturingBus(),
			api.WithTemplates(f.store), api.WithLaunchConfigs(f.store))
		router, err := api.NewRouter(api.RouterConfig{
			Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
			Auth:      alwaysAuthenticated,
			Admission: &fakeAdmitter{},
			HATEOAS:   allowAllGenerator(t),
			Routes:    []api.Route{apispec.RelaunchJob.Route(dispatcher.RelaunchJob)},
		})
		if err != nil {
			t.Fatalf("NewRouter: %v", err)
		}

		// A store that cannot answer is not the same as a job that is not
		// there, and a 404 would tell a caller their job had been deleted.
		rec := doJSON(t, router, http.MethodPost,
			"/api/v1/jobs/6ba7b810-9dad-11d1-80b4-00c04fd430c8/relaunch", "")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500: %s", rec.Code, rec.Body.String())
		}
	})
}

// launchRouter mounts the launch endpoint over a dispatcher whose
// configuration store is the caller's, so a test can fail one half of it.
func launchRouter(t *testing.T, f *templateFixture, configs api.LaunchConfigStore) http.Handler {
	t.Helper()
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus(),
		api.WithTemplates(f.store), api.WithLaunchConfigs(configs))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.LaunchTemplate.Route(dispatcher.LaunchFromTemplate)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func TestLaunch_RefusesASavedConfigurationItCannotRead(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	// A controller wired to read templates but not configurations, asked to
	// launch from one. Ignoring the reference and launching with the
	// template's defaults would run something other than what was asked
	// for, under a 202 that says it did.
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus(),
		api.WithTemplates(f.store))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.LaunchTemplate.Route(dispatcher.LaunchFromTemplate)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if rec := doJSON(t, router, http.MethodPost, launchPath(tmpl.ID), `{"config": 1}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}

func TestRelaunch_ReportsAConfigurationItCouldNotRead(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), `{"overrides":{"limit":"edge-07"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launching: status = %d: %s", rec.Code, rec.Body.String())
	}
	var launched decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	// A store that cannot answer is not the same fact as a configuration
	// that is gone: one is 500 and the other 422, and conflating them would
	// tell an operator their saved values had been deleted during an
	// outage.
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), f.jobs, newCapturingBus(),
		api.WithTemplates(f.store),
		api.WithLaunchConfigs(&faultyStore{Store: f.store, failGetConfig: errors.New("the database went away")}))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.RelaunchJob.Route(dispatcher.RelaunchJob)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if rec := doJSON(t, router, http.MethodPost,
		"/api/v1/jobs/"+launched.JobID+"/relaunch", ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
}
