package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers the Template surface over real HTTP, against a real
// ent-backed store: the administration half in templates.go and the two
// launch-side handlers in dispatcher.go, mounted through the real router at
// the real apispec patterns.
//
// It carries the phase's own Release Gate. A template with three
// prompt-able and two locked fields accepts overrides for the three,
// reports the two in ignored_fields, and rejects a survey answer violating
// its schema. That gate is about a launch, so it is asserted where a launch
// actually happens rather than one layer down: the handler is where the
// three answers (apply, report, refuse) turn into three different status
// codes, and a caller can only tell them apart from out here.

// templateFixtureSeq keeps each fixture's in-memory database distinct, for
// the reason jobStoreDBSeq exists: shared-cache SQLite gives two
// connections opened under one name the same database.
var templateFixtureSeq atomic.Int64

// templateFixture is one controller's worth of the template surface: a real
// ent client, the real store over it, and the real router mounting every
// endpoint in front of both handlers.
type templateFixture struct {
	client *ent.Client
	store  launch.Store
	jobs   dispatch.JobStore
	router http.Handler

	// Two tenants, each with an inventory, so the cross-tenant refusal has
	// somewhere to point.
	orgA, invA int
	orgB, invB int
}

func newTemplateFixture(t *testing.T) *templateFixture {
	t.Helper()

	client := newSerializedSQLiteClient(t, fmt.Sprintf("templates-%d", templateFixtureSeq.Add(1)))
	ctx := context.Background()

	orgA := client.Organization.Create().SetName("network").SaveX(ctx)
	orgB := client.Organization.Create().SetName("servers").SaveX(ctx)
	invA := client.Inventory.Create().SetName("edge routers").SetOrganization(orgA).SaveX(ctx)
	invB := client.Inventory.Create().SetName("racks").SetOrganization(orgB).SaveX(ctx)

	store := launch.NewEntStore(client)
	jobs := dispatch.NewEntJobStore(client)

	// The same concrete store reaches the Dispatcher through two narrow
	// ports and the TemplateHandler whole, exactly as cmd/controller wires
	// it. Wiring a double here would prove nothing about the tenancy
	// derivation, which lives in the store.
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "patch-edge", "other"), jobs, newCapturingBus(),
		api.WithTemplates(store), api.WithLaunchConfigs(store))
	handler := api.NewTemplateHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	router, err := api.NewRouter(api.RouterConfig{
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
			apispec.LaunchTemplate.Route(dispatcher.LaunchFromTemplate),
			apispec.ListTemplateConfigs.Route(handler.ListConfigs),
			apispec.CreateTemplateConfig.Route(handler.CreateConfig),
			apispec.RelaunchJob.Route(dispatcher.RelaunchJob),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	return &templateFixture{
		client: client, store: store, jobs: jobs, router: router,
		orgA: orgA.ID, invA: invA.ID,
		orgB: orgB.ID, invB: invB.ID,
	}
}

// decodedTemplate is what these tests read off the wire. Deliberately its
// own declaration rather than the handler's DTO: asserting against the
// handler's own struct would pass even if every JSON tag were wrong.
type decodedTemplate struct {
	ID                int            `json:"id"`
	Name              string         `json:"name"`
	Kind              string         `json:"kind"`
	KindLabel         string         `json:"kind_label"`
	Definition        string         `json:"definition"`
	Inventory         int            `json:"inventory"`
	InventoryName     string         `json:"inventory_name"`
	Organization      int            `json:"organization"`
	OrganizationName  string         `json:"organization_name"`
	Defaults          map[string]any `json:"defaults"`
	Prompts           []string       `json:"prompts"`
	AllowSimultaneous bool           `json:"allow_simultaneous"`

	Survey *struct {
		Enabled   bool `json:"enabled"`
		Questions []struct {
			Variable string `json:"variable"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"questions"`
	} `json:"survey"`
}

type decodedLaunch struct {
	Status        string `json:"status"`
	JobID         string `json:"job_id"`
	IgnoredFields []struct {
		Name   string `json:"name"`
		Layer  string `json:"layer"`
		Reason string `json:"reason"`
	} `json:"ignored_fields"`
}

type decodedConfig struct {
	ID       int            `json:"id"`
	Template int            `json:"template"`
	Name     string         `json:"name"`
	Fields   map[string]any `json:"fields"`
	Answers  map[string]any `json:"answers"`
}

// gateTemplateBody is the Release Gate's own template: three fields a
// launch may set, two it may not.
func (f *templateFixture) gateTemplateBody(name string) string {
	return fmt.Sprintf(`{
		"name": %q,
		"kind": "runbook",
		"definition": "patch-edge",
		"inventory": %d,
		"defaults": {"limit": "edge-*", "forks": 5, "verbosity": 1, "timeout": 600},
		"prompts": ["limit", "forks", "extra_vars"]
	}`, name, f.invA)
}

// createTemplate posts a template and returns what came back.
func (f *templateFixture) createTemplate(t *testing.T, body string) decodedTemplate {
	t.Helper()
	rec := doJSON(t, f.router, http.MethodPost, "/api/v1/templates", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating a template: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var got decodedTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the created template: %v", err)
	}
	return got
}

func TestTemplateAPI_ReleaseGate_AppliesWhatIsOpenAndReportsWhatIsNot(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch the edge routers"))

	// A launch supplying all five: the three the template opened, and the
	// two it locked.
	rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), `{
		"overrides": {"limit": "edge-01", "forks": 20, "extra_vars": {"version": "17.3"},
		              "verbosity": 4, "timeout": 30}
	}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}

	var launched decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatalf("decoding the launch: %v", err)
	}
	if launched.JobID == "" {
		t.Fatal("a launch that supplied two locked fields produced no job: a locked field is the template author's decision, not the operator's mistake")
	}

	// The two locked fields are reported by name, with the reason that says
	// whose decision it was.
	reported := map[string]string{}
	for _, ignored := range launched.IgnoredFields {
		reported[ignored.Name] = ignored.Reason
	}
	if len(reported) != 2 || reported["verbosity"] == "" || reported["timeout"] == "" {
		t.Fatalf("ignored_fields = %+v, want verbosity and timeout alone", launched.IgnoredFields)
	}
	for name, reason := range reported {
		if !strings.Contains(reason, "does not allow") {
			t.Errorf("%s was ignored for reason %q, want the locked reason", name, reason)
		}
	}

	// And the job that was actually created carries the template's tenant,
	// which is the column this phase gave its first writer.
	job, _, err := f.jobs.Get(context.Background(), launched.JobID)
	if err != nil {
		t.Fatalf("reading the launched job: %v", err)
	}
	if job.OrganizationID != f.orgA {
		t.Errorf("the job belongs to organization %d, want the template's %d", job.OrganizationID, f.orgA)
	}
	if job.TemplateID != tmpl.ID || job.Kind != "runbook" {
		t.Errorf("the job records template %d kind %q, want %d/runbook", job.TemplateID, job.Kind, tmpl.ID)
	}
	if job.LaunchConfigID == 0 {
		t.Error("a launch that supplied overrides recorded no configuration, so nothing could relaunch it")
	}
}

func TestTemplateAPI_ReleaseGate_RefusesASurveyAnswerViolatingItsSchema(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, fmt.Sprintf(`{
		"name": "upgrade",
		"kind": "runbook",
		"definition": "patch-edge",
		"inventory": %d,
		"prompts": ["limit"],
		"survey": {"enabled": true, "questions": [
			{"variable": "version", "label": "VERSION", "type": "multiplechoice", "required": true, "choices": ["17.3", "17.6"]}
		]}
	}`, f.invA))

	// 422 rather than 202-with-a-report, and the difference is who decided.
	// A locked field is the author's decision about what an operator may
	// change; an answer outside the offered choices means the run would
	// proceed with a variable the author said was not acceptable.
	rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), `{"answers": {"version": "99.9"}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	// A required question left unanswered is refused for the same reason.
	if rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), `{}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("launching with no answer at all: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	// And an answer the survey does accept launches.
	rec = doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID), `{"answers": {"version": "17.6"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launching with a valid answer: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

func TestTemplateAPI_APasswordAnswerNeverRoundTripsInPlaintext(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, fmt.Sprintf(`{
		"name": "rotate the vault token",
		"kind": "runbook",
		"definition": "patch-edge",
		"inventory": %d,
		"survey": {"enabled": true, "questions": [
			{"variable": "vault_token", "label": "VAULT TOKEN", "type": "password", "required": true},
			{"variable": "region", "label": "REGION", "type": "text"}
		]}
	}`, f.invA))

	const secret = "s3cret-token-value"

	// Saved deliberately through the API rather than through the store, so
	// the assertion covers the path a caller actually takes.
	rec := doJSON(t, f.router, http.MethodPost, configsPath(tmpl.ID), fmt.Sprintf(`{
		"name": "production", "answers": {"vault_token": %q, "region": "eu-west"}
	}`, secret))
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a configuration: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("the response to saving a configuration echoed the password back in plaintext")
	}

	// And reading it back.
	rec = doJSON(t, f.router, http.MethodGet, configsPath(tmpl.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("listing configurations: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("listing a template's saved configurations served the password in plaintext")
	}

	var listed struct {
		Configs []decodedConfig `json:"configs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding the configurations: %v", err)
	}
	if len(listed.Configs) != 1 {
		t.Fatalf("listed %d configurations, want 1", len(listed.Configs))
	}

	// A marker rather than a blank: empty and withheld are different facts,
	// and a form rendering the empty string would clear the stored answer
	// on the next save.
	if got := listed.Configs[0].Answers["vault_token"]; got != launch.RedactedMarker {
		t.Errorf("vault_token reads as %v, want the redaction marker", got)
	}
	// The non-secret answer is untouched: redaction is driven by the
	// survey's declared question types, not by guessing from the value.
	if got := listed.Configs[0].Answers["region"]; got != "eu-west" {
		t.Errorf("region reads as %v, want the answer somebody gave", got)
	}

	// The value is nevertheless really there, which is what makes the
	// assertion above about the projection rather than about a lost write.
	stored, err := f.store.SavedConfigs(context.Background(), tmpl.ID)
	if err != nil {
		t.Fatalf("reading the stored configurations: %v", err)
	}
	if got := stored[0].Answers["vault_token"]; got != secret {
		t.Errorf("the stored answer is %v, want the value somebody typed: redaction must be a projection, not a write", got)
	}
}

func TestTemplateAPI_CopyTakesTheSurveyAndLeavesTheSavedConfigurations(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, fmt.Sprintf(`{
		"name": "patch",
		"kind": "runbook",
		"definition": "patch-edge",
		"inventory": %d,
		"defaults": {"forks": 5},
		"prompts": ["limit"],
		"survey": {"enabled": true, "questions": [
			{"variable": "region", "label": "REGION", "type": "text"}
		]}
	}`, f.invA))

	if rec := doJSON(t, f.router, http.MethodPost, configsPath(tmpl.ID),
		`{"name": "eu", "answers": {"region": "eu-west"}}`); rec.Code != http.StatusCreated {
		t.Fatalf("saving a configuration: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, f.router, http.MethodPost, "/api/v1/templates/"+strconv.Itoa(tmpl.ID)+"/copy", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var copied decodedTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &copied); err != nil {
		t.Fatalf("decoding the copy: %v", err)
	}

	if copied.ID == tmpl.ID {
		t.Fatal("copying a template returned the template it copied")
	}
	if copied.Name != "patch (copy)" {
		t.Errorf("the copy is named %q, want a derived name", copied.Name)
	}
	if copied.Definition != "patch-edge" || copied.Inventory != f.invA {
		t.Errorf("the copy runs %q against inventory %d, want the original's", copied.Definition, copied.Inventory)
	}
	// The survey travels: the questions are part of how the template runs.
	if copied.Survey == nil || len(copied.Survey.Questions) != 1 {
		t.Errorf("the copy carries survey %+v, want the original's question", copied.Survey)
	}

	// The saved configurations do not. They are one operator's answers,
	// secrets among them, and duplicating them would move a stored password
	// onto an object with its own separate access grants.
	configs, err := f.store.SavedConfigs(context.Background(), copied.ID)
	if err != nil {
		t.Fatalf("reading the copy's configurations: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("the copy carries %d saved configurations, want none", len(configs))
	}

	// Copying again collides on the derived name, which is a 409 rather
	// than a second template nobody can tell from the first.
	if rec := doJSON(t, f.router, http.MethodPost, "/api/v1/templates/"+strconv.Itoa(tmpl.ID)+"/copy", ""); rec.Code != http.StatusConflict {
		t.Errorf("copying twice: status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
}

func TestTemplateAPI_RefusesATemplateNamingAnotherTenantsInventory(t *testing.T) {
	f := newTemplateFixture(t)

	// The submission is well formed and the caller is authenticated. What
	// it is not is entitled: a template in one tenant that dispatches
	// against another tenant's fleet is the shape every individual check
	// would pass (FAILURE_PATTERNS.md #97), and the organization is derived
	// from the inventory precisely so it cannot be asserted around.
	rec := doJSON(t, f.router, http.MethodPost, "/api/v1/templates", fmt.Sprintf(`{
		"name": "reach across", "kind": "runbook", "definition": "patch-edge", "inventory": %d
	}`, f.invB))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: a template naming its own tenant's inventory is legitimate: %s", rec.Code, rec.Body.String())
	}

	var created decodedTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if created.Organization != f.orgB {
		t.Errorf("the template belongs to organization %d, want the inventory's %d: tenancy is derived, never submitted",
			created.Organization, f.orgB)
	}
	if created.OrganizationName != "servers" || created.InventoryName != "racks" {
		t.Errorf("the template renders %q/%q, want the names beside the ids", created.OrganizationName, created.InventoryName)
	}
}

func TestTemplateAPI_RefusesWhatNoLaunchCouldFix(t *testing.T) {
	f := newTemplateFixture(t)

	cases := map[string]struct {
		body       string
		wantStatus int
	}{
		// A kind nothing registers cannot be run by anybody, and defaulting
		// it would hand a file to an executor that cannot read it.
		"an unregistered kind": {
			body:       fmt.Sprintf(`{"name": "a", "kind": "terraform", "definition": "x", "inventory": %d}`, f.invA),
			wantStatus: http.StatusBadRequest,
		},
		// A promptable field the kind has no field for would render as a
		// control on the launch form and be dropped by the resolver.
		"a prompt for a field the kind has not got": {
			body:       fmt.Sprintf(`{"name": "b", "kind": "runbook", "definition": "x", "inventory": %d, "prompts": ["become_password"]}`, f.invA),
			wantStatus: http.StatusBadRequest,
		},
		// No inventory means no organization to inherit, which is exactly
		// the state that left Job.organization_id unwritten.
		"no inventory": {
			body:       `{"name": "c", "kind": "runbook", "definition": "x"}`,
			wantStatus: http.StatusBadRequest,
		},
		"an inventory that does not exist": {
			body:       `{"name": "d", "kind": "runbook", "definition": "x", "inventory": 4242}`,
			wantStatus: http.StatusNotFound,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := doJSON(t, f.router, http.MethodPost, "/api/v1/templates", tc.body)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestTemplateAPI_UpdateCannotRepointATemplateAtDifferentCode(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	rec := doJSON(t, f.router, http.MethodPatch, "/api/v1/templates/"+strconv.Itoa(tmpl.ID), fmt.Sprintf(`{
		"name": "patch", "kind": "playbook", "definition": "other", "inventory": %d,
		"defaults": {"limit": "edge-*"}, "prompts": ["limit"]
	}`, f.invB))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var updated decodedTemplate
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	// The editable half changed; the three that decide what actually runs
	// did not. Re-pointing a template at different code while it keeps its
	// name, its grants and its job history is how a reviewed thing quietly
	// becomes an unreviewed one.
	if updated.Kind != "runbook" || updated.Definition != "patch-edge" || updated.Inventory != f.invA {
		t.Errorf("the update re-pointed the template to %s/%s/inventory %d",
			updated.Kind, updated.Definition, updated.Inventory)
	}
	if len(updated.Defaults) != 1 {
		t.Errorf("defaults = %v, want the submitted single value", updated.Defaults)
	}
}

func TestRelaunch_RepeatsTheConfigurationTheJobRanWith(t *testing.T) {
	f := newTemplateFixture(t)
	tmpl := f.createTemplate(t, f.gateTemplateBody("patch"))

	rec := doJSON(t, f.router, http.MethodPost, launchPath(tmpl.ID),
		`{"overrides": {"limit": "edge-07", "forks": 11}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launching: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var first decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decoding the launch: %v", err)
	}

	rec = doJSON(t, f.router, http.MethodPost, "/api/v1/jobs/"+first.JobID+"/relaunch", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("relaunching: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var second decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decoding the relaunch: %v", err)
	}
	if second.JobID == first.JobID {
		t.Fatal("relaunching returned the job it was repeating")
	}

	// The repeat carries the values the original ran with, which is the
	// difference between relaunching a job and merely running its template
	// again.
	ctx := context.Background()
	job, _, err := f.jobs.Get(ctx, second.JobID)
	if err != nil {
		t.Fatalf("reading the relaunched job: %v", err)
	}
	if job.LaunchConfigID == 0 {
		t.Fatal("the relaunched job records no configuration")
	}
	repeated, err := f.store.GetConfig(ctx, job.LaunchConfigID)
	if err != nil {
		t.Fatalf("reading the repeated configuration: %v", err)
	}
	if repeated.Fields.String("limit") != "edge-07" || repeated.Fields.Int("forks") != 11 {
		t.Errorf("the relaunch ran with %v, want the original's limit and forks", repeated.Fields)
	}
}

func TestRelaunch_RefusesWhatItCannotRepeat(t *testing.T) {
	f := newTemplateFixture(t)
	ctx := context.Background()

	// A job launched before templates existed, by naming a group and a
	// runbook. There is no template to run again, and inventing one would
	// mean guessing which saved definition somebody meant.
	legacy := &dispatch.Job{RunbookID: "patch-edge", GroupName: "routers", Actor: "ada@example.com"}
	if err := f.jobs.Create(ctx, legacy); err != nil {
		t.Fatalf("creating a pre-template job: %v", err)
	}
	rec := doJSON(t, f.router, http.MethodPost, "/api/v1/jobs/"+legacy.JobID+"/relaunch", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("relaunching a job with no template: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}

	// A job that answered a password question. The answer is stored and
	// could be replayed, which is exactly why this is a decision rather
	// than a limitation: replaying it would let anybody who can relaunch
	// cause a secret they have never seen to be used again under their own
	// name.
	secret := f.createTemplate(t, fmt.Sprintf(`{
		"name": "rotate", "kind": "runbook", "definition": "patch-edge", "inventory": %d,
		"survey": {"enabled": true, "questions": [
			{"variable": "vault_token", "label": "VAULT TOKEN", "type": "password", "required": true}
		]}
	}`, f.invA))

	rec = doJSON(t, f.router, http.MethodPost, launchPath(secret.ID), `{"answers": {"vault_token": "hunter2"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("launching with a password answer: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var launched decodedLaunch
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	rec = doJSON(t, f.router, http.MethodPost, "/api/v1/jobs/"+launched.JobID+"/relaunch", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("relaunching a job that answered a password question: status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "hunter2") {
		t.Fatal("the refusal echoed the secret it was refusing to replay")
	}
	if body := rec.Body.String(); !strings.Contains(body, "vault_token") {
		t.Errorf("the refusal says %q, want it to name the question a caller has to answer again", body)
	}

	// A job id that names nothing is a 404, distinguishable from a job that
	// exists and cannot be repeated.
	if rec := doJSON(t, f.router, http.MethodPost,
		"/api/v1/jobs/6ba7b810-9dad-11d1-80b4-00c04fd430c8/relaunch", ""); rec.Code != http.StatusNotFound {
		t.Errorf("relaunching a job that does not exist: status = %d, want 404", rec.Code)
	}
}

func TestLaunch_RefusesASavedConfigurationBelongingToAnotherTemplate(t *testing.T) {
	f := newTemplateFixture(t)
	mine := f.createTemplate(t, f.gateTemplateBody("mine"))
	theirs := f.createTemplate(t, fmt.Sprintf(`{
		"name": "theirs", "kind": "runbook", "definition": "other", "inventory": %d, "prompts": ["limit"]
	}`, f.invB))

	rec := doJSON(t, f.router, http.MethodPost, configsPath(theirs.ID), `{"name": "theirs", "fields": {"limit": "rack-*"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("saving a configuration: status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var saved decodedConfig
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	// A configuration is only meaningful against the prompts its own
	// template declared, and these two belong to different tenants.
	rec = doJSON(t, f.router, http.MethodPost, launchPath(mine.ID), fmt.Sprintf(`{"config": %d}`, saved.ID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}

	// Its own template launches from it.
	if rec := doJSON(t, f.router, http.MethodPost, launchPath(theirs.ID),
		fmt.Sprintf(`{"config": %d}`, saved.ID)); rec.Code != http.StatusAccepted {
		t.Fatalf("launching from its own template's configuration: status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

func TestTemplateAPI_ListAndDelete(t *testing.T) {
	f := newTemplateFixture(t)
	first := f.createTemplate(t, f.gateTemplateBody("patch the edge routers"))
	f.createTemplate(t, fmt.Sprintf(`{"name": "reboot", "kind": "runbook", "definition": "other", "inventory": %d}`, f.invB))

	rec := doJSON(t, f.router, http.MethodGet, "/api/v1/templates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var listed struct {
		Templates []decodedTemplate `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(listed.Templates) != 2 {
		t.Fatalf("listed %d templates, want 2", len(listed.Templates))
	}
	for _, tmpl := range listed.Templates {
		// A listing renders names, not foreign keys, and carries no survey:
		// a list of twenty templates should not cost two hundred questions
		// no column shows.
		if tmpl.InventoryName == "" || tmpl.OrganizationName == "" {
			t.Errorf("template %q renders bare ids %d/%d", tmpl.Name, tmpl.Inventory, tmpl.Organization)
		}
		if tmpl.Survey != nil {
			t.Errorf("template %q carried its survey into a listing", tmpl.Name)
		}
		if tmpl.KindLabel != "Runbook" {
			t.Errorf("template %q renders kind label %q, want the descriptor's own", tmpl.Name, tmpl.KindLabel)
		}
	}

	// Narrowing by tenant is one query parameter rather than a second
	// endpoint.
	rec = doJSON(t, f.router, http.MethodGet, "/api/v1/templates?organization="+strconv.Itoa(f.orgB), "")
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(listed.Templates) != 1 || listed.Templates[0].Name != "reboot" {
		t.Errorf("narrowing to organization %d returned %+v", f.orgB, listed.Templates)
	}

	if rec := doJSON(t, f.router, http.MethodDelete, "/api/v1/templates/"+strconv.Itoa(first.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("deleting: status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, f.router, http.MethodGet, "/api/v1/templates/"+strconv.Itoa(first.ID), ""); rec.Code != http.StatusNotFound {
		t.Errorf("reading a deleted template: status = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, f.router, http.MethodPost, launchPath(first.ID), ""); rec.Code != http.StatusNotFound {
		t.Errorf("launching a deleted template: status = %d, want 404", rec.Code)
	}
}

func launchPath(templateID int) string {
	return "/api/v1/templates/" + strconv.Itoa(templateID) + "/launch"
}

func configsPath(templateID int) string {
	return "/api/v1/templates/" + strconv.Itoa(templateID) + "/configs"
}
