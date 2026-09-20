// This file covers what the schedule write path refuses, over real HTTP and
// against a real store.
//
// Every case here used to be either impossible to state or discovered
// unattended. The first is the one that matters most and it is a security
// answer: writing a schedule needed only schedule:write, so a token holding
// that and nothing else could arrange for any template in the deployment to run
// for real, repeatedly, under nobody's supervision (FAILURE_PATTERNS.md #268).
// The rest are choices that could only ever fail, saved cleanly and then failing
// silently at whatever hour the recurrence named.
//
// The store here is the real one, over a real database, behind the real router.
// A stub store would prove the handler passes an argument along and nothing at
// all about whether the refusals happen, which is the whole subject.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types/jobtemplate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types/projectsync"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

// scheduleFixtureSeq keeps each fixture's in-memory database distinct.
var scheduleFixtureSeq atomic.Int64

// launchableScheduleFixture is one controller's worth of the schedule write
// path, over real stores.
type launchableScheduleFixture struct {
	client *ent.Client
	router http.Handler

	// store is the real schedule store, exposed for the one test that builds a
	// second handler over it.
	store api.ScheduleStore

	templateLaunchable int
	templateID         int
	projectLaunchable  int
	manualLaunchable   int
	savedConfigID      int
	otherOrgLaunchable int
	organizationID     int
}

// newLaunchableScheduleFixture wires the real launch, project, launchable and
// schedule stores behind the real HTTP router.
//
// The router's authenticator is supplied per test, because the scope the caller
// holds is exactly what several of these cases are about.
func newLaunchableScheduleFixture(t *testing.T, identity *auth.Identity) *launchableScheduleFixture {
	t.Helper()
	ctx := context.Background()

	client := newSerializedSQLiteClient(t, fmt.Sprintf("schedlaunch-%d", scheduleFixtureSeq.Add(1)))
	org := client.Organization.Create().SetName("network").SaveX(ctx)
	other := client.Organization.Create().SetName("finance").SaveX(ctx)
	inv := client.Inventory.Create().SetName("edge").SetOrganization(org).SaveX(ctx)
	otherInv := client.Inventory.Create().SetName("ledgers").SetOrganization(other).SaveX(ctx)

	templates := launch.NewEntStore(client, launch.StaticCatalog(
		launch.CatalogEntry{Kind: "runbook", Definition: "patch-edge"},
	))
	tmpl, err := templates.Create(ctx, launch.Template{
		Name: "patch the edge", KindName: "runbook", Definition: "patch-edge", InventoryID: inv.ID,
	})
	if err != nil {
		t.Fatalf("creating a template: %v", err)
	}
	otherTmpl, err := templates.Create(ctx, launch.Template{
		Name: "close the books", KindName: "runbook", Definition: "patch-edge", InventoryID: otherInv.ID,
	})
	if err != nil {
		t.Fatalf("creating another organization's template: %v", err)
	}

	// A saved configuration belonging to the OTHER template, which is what
	// makes the cross-template refusal testable.
	saved, err := templates.SaveConfig(ctx, launch.SavedConfig{
		TemplateID: otherTmpl.ID,
		Name:       "somebody elses",
		Fields:     launch.Fields{"limit": "theirs-*"},
	})
	if err != nil {
		t.Fatalf("saving a configuration: %v", err)
	}

	projects := project.NewEntStore(client, project.SourcePolicy{})
	syncable, err := projects.Create(ctx, project.Project{
		Name: "automation", SCMType: project.SCMGit,
		SCMURL: "https://example.invalid/automation.git", OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("creating a project: %v", err)
	}
	manual, err := projects.Create(ctx, project.Project{
		Name: "hand-managed", SCMType: project.SCMManual, OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("creating a manual project: %v", err)
	}

	// The real launchers behind the real router, so a preflight refusal is the
	// launcher's own rather than a stub's idea of one.
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(templates), api.WithLaunchConfigs(templates))
	runner := project.NewRunner(projects, stubSyncer{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = runner.Shutdown(ctx) })

	router, err := launchable.NewRouter(map[string]launchable.Launcher{
		jobtemplate.Type: dispatcher,
		projectsync.Type: runner,
	})
	if err != nil {
		t.Fatalf("building the launchable router: %v", err)
	}

	store := schedule.NewEntStore(client, launchable.Admission{
		Store:  launchable.NewEntStore(client),
		Router: router,
	})
	handler := api.NewScheduleHandler(store, templates, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	httpRouter, err := api.NewRouter(api.RouterConfig{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if identity == nil {
					next.ServeHTTP(w, r)
					return
				}
				next.ServeHTTP(w, r.WithContext(contextWithIdentity(r, identity)))
			})
		},
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.CreateSchedule.Route(handler.Create),
			apispec.GetSchedule.Route(handler.Get),
			apispec.UpdateSchedule.Route(handler.Update),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	return &launchableScheduleFixture{
		client:             client,
		router:             httpRouter,
		store:              store,
		templateLaunchable: tmpl.LaunchableID,
		templateID:         tmpl.ID,
		projectLaunchable:  syncable.LaunchableID,
		manualLaunchable:   manual.LaunchableID,
		savedConfigID:      saved.ID,
		otherOrgLaunchable: otherTmpl.LaunchableID,
		organizationID:     org.ID,
	}
}

// stubSyncer stands in for a clone. Nothing here starts one: every case is a
// refusal, and the one success asserts what was stored rather than what ran.
type stubSyncer struct{}

func (stubSyncer) Sync(context.Context, project.Project, io.Writer) (project.Result, error) {
	return project.Result{Status: project.SyncSucceeded}, nil
}

func (stubSyncer) Playbooks(context.Context, project.Project) ([]string, error) { return nil, nil }

// post writes a schedule and returns the status and body.
func (f *launchableScheduleFixture) post(t *testing.T, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// scheduleBody is a valid create body naming one launchable.
func scheduleBody(name string, launchableID int, extra string) string {
	body := fmt.Sprintf(
		`{"name":%q,"unified_job_template":%d,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"%s}`,
		name, launchableID, extra)
	return body
}

// operator is an identity holding the named scopes and nothing else. Role
// operator rather than admin, deliberately: the admin role bypasses scope
// checks entirely, so an admin would pass every case here for the wrong reason.
func operator(scopes ...auth.Scope) *auth.Identity {
	return &auth.Identity{Subject: "operator", Role: auth.RoleOperator, Scopes: scopes}
}

// TestSchedules_WritingOneNeedsPermissionToLaunchWhatItLaunches is finding
// #268's proof.
//
// A token holding schedule:write and nothing else may write schedules and may
// not launch anything, which is a supported configuration: auth/scopes.go
// presents the two as separately grantable, on the argument that deciding WHEN
// something runs and being allowed to run it are different privileges. What was
// missing is that the first must not be a way around the second.
func TestSchedules_WritingOneNeedsPermissionToLaunchWhatItLaunches(t *testing.T) {
	f := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite))

	status, body := f.post(t, scheduleBody("nightly", f.templateLaunchable, ""))
	if status != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for a caller who may not launch the template; body %s", status, body)
	}
	if !strings.Contains(body, string(auth.ScopeRunbookExecute)) {
		t.Errorf("the refusal does not name the scope that is missing: %s", body)
	}

	// Nothing was stored, so the refusal is a refusal rather than a message
	// beside a schedule that will fire anyway.
	if n := f.client.Schedule.Query().CountX(context.Background()); n != 0 {
		t.Errorf("%d schedules were written despite the refusal", n)
	}

	// The same caller, holding the scope the template's own type declares,
	// succeeds. That is what makes this a check rather than a ban.
	allowed := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute))
	if status, body := allowed.post(t, scheduleBody("nightly", allowed.templateLaunchable, "")); status != http.StatusCreated {
		t.Errorf("status %d for a caller holding runbook:execute, want 201; body %s", status, body)
	}
}

// TestSchedules_EachTypeNeedsItsOwnScope proves the check reads the type's own
// declaration rather than one blanket permission: syncing a project is
// project:write, and holding runbook:execute does not grant it.
func TestSchedules_EachTypeNeedsItsOwnScope(t *testing.T) {
	f := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute))

	status, body := f.post(t, scheduleBody("nightly-sync", f.projectLaunchable, ""))
	if status != http.StatusForbidden {
		t.Fatalf("status %d, want 403: runbook:execute does not grant syncing a project; body %s", status, body)
	}
	if !strings.Contains(body, string(auth.ScopeProjectWrite)) {
		t.Errorf("the refusal does not name project:write: %s", body)
	}

	allowed := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeProjectWrite))
	if status, body := allowed.post(t, scheduleBody("nightly-sync", allowed.projectLaunchable, "")); status != http.StatusCreated {
		t.Errorf("status %d for a caller holding project:write, want 201; body %s", status, body)
	}
}

// TestSchedules_RefusalsThatUsedToSurfaceAtThreeInTheMorning covers every case
// the write path now answers rather than storing and discovering later.
func TestSchedules_RefusalsThatUsedToSurfaceAtThreeInTheMorning(t *testing.T) {
	cases := []struct {
		name       string
		body       func(f *launchableScheduleFixture) string
		wantStatus int
		wantIn     string
	}{
		{
			name: "a saved configuration belonging to another template",
			body: func(f *launchableScheduleFixture) string {
				return scheduleBody("nightly", f.templateLaunchable,
					fmt.Sprintf(`,"saved_config":%d`, f.savedConfigID))
			},
			wantStatus: http.StatusBadRequest,
			wantIn:     "saved_config",
		},
		{
			name: "a saved configuration on a project sync, which takes none",
			body: func(f *launchableScheduleFixture) string {
				return scheduleBody("nightly", f.projectLaunchable,
					fmt.Sprintf(`,"saved_config":%d`, f.savedConfigID))
			},
			wantStatus: http.StatusBadRequest,
			wantIn:     "saved_config",
		},
		{
			name: "a project with nothing to fetch",
			body: func(f *launchableScheduleFixture) string {
				return scheduleBody("nightly", f.manualLaunchable, "")
			},
			wantStatus: http.StatusBadRequest,
			wantIn:     "no fetchable source",
		},
		{
			name: "something that does not exist",
			body: func(f *launchableScheduleFixture) string {
				return scheduleBody("nightly", 99999, "")
			},
			wantStatus: http.StatusNotFound,
			wantIn:     launchable.TargetField,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLaunchableScheduleFixture(t, operator(
				auth.ScopeScheduleWrite, auth.ScopeRunbookExecute, auth.ScopeProjectWrite))

			status, body := f.post(t, tc.body(f))
			if status != tc.wantStatus {
				t.Fatalf("status %d, want %d; body %s", status, tc.wantStatus, body)
			}
			if tc.wantIn != "" && !strings.Contains(body, tc.wantIn) {
				t.Errorf("the refusal does not mention %q: %s", tc.wantIn, body)
			}
		})
	}
}

// TestSchedules_ATargetInAnotherOrganizationIsStillAccepted states a LIMIT
// rather than a behavior anybody wanted, because leaving it unstated would let
// a reader assume the tenancy half of the check does more than it does.
//
// No request in this build carries a tenant: every API caller is unnarrowed
// (schedule.AnyOrganization), so the caller's own organization cannot be
// compared to the target's. What IS enforced is that the schedule lands in its
// TARGET's organization rather than in one the submission named, so a schedule
// and what it launches can never disagree about whose work is running. The
// missing half is enforced at the store for a narrowed caller, which the UI and
// a future tenant-aware request both go through, and is covered in
// internal/schedule.
func TestSchedules_ATargetInAnotherOrganizationIsStillAccepted(t *testing.T) {
	f := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute))

	status, body := f.post(t, scheduleBody("cross", f.otherOrgLaunchable, ""))
	if status != http.StatusCreated {
		t.Fatalf("status %d, want 201: no request carries a tenant yet; body %s", status, body)
	}

	var created struct {
		Organization int `json:"organization"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if created.Organization == f.organizationID {
		t.Errorf("the schedule landed in organization %d, the same one the other fixtures use; "+
			"it must take its target's organization so the two cannot disagree", created.Organization)
	}
}

// TestSchedules_AnUnattributableWriteIsRefused proves the reach comes from the
// request's identity and that a request carrying none is refused rather than
// treated as unrestricted.
func TestSchedules_AnUnattributableWriteIsRefused(t *testing.T) {
	f := newLaunchableScheduleFixture(t, nil)

	status, body := f.post(t, scheduleBody("nightly", f.templateLaunchable, ""))
	if status != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 for a request with no identity; body %s", status, body)
	}
	if n := f.client.Schedule.Query().CountX(context.Background()); n != 0 {
		t.Errorf("%d schedules were written for a caller nobody authenticated", n)
	}
}

// TestSchedules_TheDeprecatedTemplateFieldStillWorks covers the compatibility
// path, and the one case it must not silently resolve: two fields naming
// different things.
func TestSchedules_TheDeprecatedTemplateFieldStillWorks(t *testing.T) {
	f := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute))

	// The old spelling names a TEMPLATE id and is resolved to its launchable.
	status, body := f.post(t, fmt.Sprintf(
		`{"name":"old-client","template":%d,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`,
		f.templateID))
	if status != http.StatusCreated {
		t.Fatalf("status %d for the deprecated field, want 201; body %s", status, body)
	}

	var created struct {
		UnifiedJobTemplate     int    `json:"unified_job_template"`
		UnifiedJobTemplateName string `json:"unified_job_template_name"`
		UnifiedJobTemplateType string `json:"unified_job_template_type"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decoding the response: %v", err)
	}
	if created.UnifiedJobTemplate != f.templateLaunchable {
		t.Errorf("stored launchable %d, want the template's own %d",
			created.UnifiedJobTemplate, f.templateLaunchable)
	}
	if created.UnifiedJobTemplateName == "" || created.UnifiedJobTemplateType != jobtemplate.Type {
		t.Errorf("response describes the target as %q/%q, want its name and its type",
			created.UnifiedJobTemplateName, created.UnifiedJobTemplateType)
	}

	// Both fields, disagreeing: refused rather than resolved one way. A client
	// updated by halves must not silently repoint a schedule.
	status, body = f.post(t, fmt.Sprintf(
		`{"name":"confused","template":%d,"unified_job_template":%d,`+
			`"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`,
		f.templateID, f.otherOrgLaunchable))
	if status != http.StatusBadRequest {
		t.Errorf("status %d for two fields naming different things, want 400; body %s", status, body)
	}

	// Both fields agreeing is accepted, so the refusal is about disagreement
	// rather than about sending both.
	status, body = f.post(t, fmt.Sprintf(
		`{"name":"agreeing","template":%d,"unified_job_template":%d,`+
			`"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`,
		f.templateID, f.templateLaunchable))
	if status != http.StatusCreated {
		t.Errorf("status %d for two fields naming the same thing, want 201; body %s", status, body)
	}
}

// TestSchedules_TheDeprecatedFieldsRefusals covers what the compatibility path
// does when it cannot resolve what it was given.
//
// Both answers are about the old field rather than about schedules, which is
// why they are worth separating: a caller still using it needs to know whether
// the template is missing or whether this deployment cannot resolve one at all.
func TestSchedules_TheDeprecatedFieldsRefusals(t *testing.T) {
	f := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute))

	// A template id that names nothing.
	status, body := f.post(t, `{"name":"gone","template":99999,`+
		`"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`)
	if status != http.StatusNotFound {
		t.Errorf("status %d for a template that does not exist, want 404; body %s", status, body)
	}

	// A template with no launchable row, which is what a database the
	// launchable migration has not reached looks like. Written straight to the
	// database, because the store cannot produce one.
	ctx := context.Background()
	org := f.client.Organization.Query().FirstX(ctx)
	inv := f.client.Inventory.Query().FirstX(ctx)
	orphan := f.client.Template.Create().
		SetName("unmigrated").
		SetKind("runbook").
		SetDefinition("patch-edge").
		SetOrganization(org).
		SetInventory(inv).
		SaveX(ctx)

	status, body = f.post(t, fmt.Sprintf(
		`{"name":"unmigrated","template":%d,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`,
		orphan.ID))
	if status != http.StatusConflict {
		t.Errorf("status %d for a template with no launchable row, want 409; body %s", status, body)
	}
	if !strings.Contains(body, "migrations") {
		t.Errorf("the refusal does not say what is actually wrong: %s", body)
	}
}

// TestSchedules_AHandlerWithNoTemplateReaderRefusesTheDeprecatedField proves the
// alias is refused rather than silently ignored where it cannot be resolved.
//
// Silently ignoring it would repoint nothing and answer 201, which is the worst
// of the three possible behaviors: the caller believes a schedule now launches
// something it does not.
func TestSchedules_AHandlerWithNoTemplateReaderRefusesTheDeprecatedField(t *testing.T) {
	f := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute))
	handler := api.NewScheduleHandler(f.store, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.CreateSchedule.Route(handler.Create)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(
		`{"name":"old","template":42,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unified_job_template") {
		t.Errorf("the refusal does not name the field to use instead: %s", rec.Body.String())
	}
}

// TestSchedules_TheHandlersOwnIdentityGuard covers a guard the router's own
// middleware normally reaches first.
//
// Writing a schedule needs an identity, because the reach it checks is that
// caller's. The route requires a scope, so the admission middleware refuses an
// unauthenticated request before any handler runs, which is what the
// router-level test above actually proves. This calls the handlers directly, so
// the guard inside them is exercised rather than assumed: it is what stands
// between a mounting mistake and a schedule written with nobody's permissions.
func TestSchedules_TheHandlersOwnIdentityGuard(t *testing.T) {
	f := newLaunchableScheduleFixture(t, operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute))
	handler := api.NewScheduleHandler(f.store, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	body := scheduleBody("unattributed", f.templateLaunchable, "")

	// Create, with no identity on the context at all.
	create := httptest.NewRequest(http.MethodPost, "/api/v1/schedules", strings.NewReader(body))
	create.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.Create(rec, create)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Create with no identity = %d, want 401; body %s", rec.Code, rec.Body.String())
	}

	// Update, which asks the same question on its own path.
	update := httptest.NewRequest(http.MethodPatch, "/api/v1/schedules/whatever", strings.NewReader(body))
	update.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.Update(rec, update)
	if rec.Code == http.StatusOK {
		t.Errorf("Update with no identity = 200, want it refused; body %s", rec.Body.String())
	}

	if n := f.client.Schedule.Query().CountX(context.Background()); n != 0 {
		t.Errorf("%d schedules were written with no identity on the request", n)
	}
}
