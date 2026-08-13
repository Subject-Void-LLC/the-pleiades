package launch_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers the ent-backed store against a real in-memory database,
// per RULE 0. Three of the behaviours here belong to the schema or to the
// query rather than to the Go code above them and could not be observed
// through a mock: the composite unique index on (name, organization), the
// cascade that takes a survey with its template, and the tenancy check that
// derives a template's organization from its inventory.

type storeFixture struct {
	store  launch.Store
	client *ent.Client

	// Two tenants, each with an inventory, so a cross-tenant assertion has
	// somewhere to point.
	orgA, invA int
	orgB, invB int
}

func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()

	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:launchstore%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	orgA := client.Organization.Create().SetName("network").SaveX(ctx)
	orgB := client.Organization.Create().SetName("servers").SaveX(ctx)
	invA := client.Inventory.Create().SetName("edge").SetOrganization(orgA).SaveX(ctx)
	invB := client.Inventory.Create().SetName("racks").SetOrganization(orgB).SaveX(ctx)

	return storeFixture{
		// The catalog lists exactly what this suite creates, so the
		// existence check is exercised for real rather than stubbed out:
		// a fixture template naming anything else fails loudly.
		store: launch.NewEntStore(client, launch.StaticCatalog(
			launch.CatalogEntry{Kind: "runbook", Definition: "patch-edge"},
		)),
		client: client,
		orgA:   orgA.ID, invA: invA.ID,
		orgB: orgB.ID, invB: invB.ID,
	}
}

// template builds a valid runbook template against f's first tenant.
func (f storeFixture) template(name string) launch.Template {
	return launch.Template{
		Name:        name,
		KindName:    "runbook",
		Definition:  "patch-edge",
		InventoryID: f.invA,
		Defaults:    launch.Fields{"limit": "edge-*", "forks": 5},
		Prompts:     []string{"limit"},
	}
}

func TestStore_ATemplateTakesItsTenantFromItsInventory(t *testing.T) {
	f := newStoreFixture(t)

	// The submission names no organization at all. It gets one anyway,
	// derived from the inventory, which is the whole point: a caller who
	// could set it directly could tag their jobs with somebody else's
	// tenant, and Job.organization_id has had no writer precisely because
	// nothing upstream of a dispatch carried one.
	created, err := f.store.Create(context.Background(), f.template("patch"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.OrganizationID != f.orgA {
		t.Errorf("the template belongs to organization %d, want the inventory's %d", created.OrganizationID, f.orgA)
	}

	// And the names come back beside the ids, off edges the query already
	// loaded, so a list does not render two integers.
	if created.OrganizationName != "network" || created.InventoryName != "edge" {
		t.Errorf("the template carries names %q and %q, want the loaded edges' own",
			created.OrganizationName, created.InventoryName)
	}
}

func TestStore_RefusesATemplateNamingAnotherTenantsInventory(t *testing.T) {
	f := newStoreFixture(t)

	tmpl := f.template("patch")
	tmpl.OrganizationID = f.orgA // this tenant
	tmpl.InventoryID = f.invB    // somebody else's fleet

	// The most security-relevant refusal in the package. Every individual
	// step of the attack passes its own check: the caller may administer
	// organization A, the inventory exists, the template validates. Only
	// the relationship between them is wrong, which is the shape
	// FAILURE_PATTERNS.md #97 records.
	_, err := f.store.Create(context.Background(), tmpl)
	if !errors.Is(err, launch.ErrCrossTenant) {
		t.Fatalf("Create across tenants returned %v, want ErrCrossTenant", err)
	}

	// The side effect is the assertion, not the status.
	if n := f.client.Template.Query().CountX(context.Background()); n != 0 {
		t.Errorf("a refused cross-tenant template left %d rows behind", n)
	}
}

func TestStore_ANameIsUniqueWithinATenantAndNotAcrossThem(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	if _, err := f.store.Create(ctx, f.template("patch")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := f.store.Create(ctx, f.template("patch")); !errors.Is(err, launch.ErrExists) {
		t.Errorf("a duplicate name in one tenant returned %v, want ErrExists", err)
	}

	// Two tenants both having a "patch" template is the ordinary case, and
	// the constraint belongs to the schema rather than to the Go code above
	// it, so this is asserted through a real database.
	other := f.template("patch")
	other.InventoryID = f.invB
	if _, err := f.store.Create(ctx, other); err != nil {
		t.Errorf("a second tenant could not reuse a template name: %v", err)
	}
}

func TestStore_ASurveyRoundTripsInItsAuthoredOrder(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	tmpl := f.template("patch")
	tmpl.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "target_version", Label: "Version", Type: launch.QuestionChoice,
			Required: true, Choices: []string{"17.3", "17.6"}},
		{Variable: "batch", Label: "Batch size", Type: launch.QuestionInteger, Min: 1, Max: 50, Default: "10"},
		{Variable: "vault_token", Label: "Vault token", Type: launch.QuestionPassword},
	}}

	created, err := f.store.Create(ctx, tmpl)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The order is authored, not incidental: a question that only makes
	// sense after another has been answered has to render after it, and a
	// query with no ORDER BY returns rows in whatever order the storage
	// engine feels like.
	got := created.Survey.Questions
	if len(got) != 3 {
		t.Fatalf("the survey came back with %d questions, want 3", len(got))
	}
	for i, want := range []string{"target_version", "batch", "vault_token"} {
		if got[i].Variable != want {
			t.Errorf("question %d is %q, want %q: the authored order was not preserved", i, got[i].Variable, want)
		}
	}
	if len(got[0].Choices) != 2 || got[1].Default != "10" || !got[2].Type.Secret() {
		t.Errorf("a question lost part of its schema on the round trip: %+v", got)
	}
}

func TestStore_UpdateReplacesTheSurveyAndKeepsWhatRuns(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	tmpl := f.template("patch")
	tmpl.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "old", Label: "Old", Type: launch.QuestionText},
	}}
	created, err := f.store.Create(ctx, tmpl)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	edit := created
	edit.Name = "patch renamed"
	edit.Survey.Questions = []launch.Question{{Variable: "new", Label: "New", Type: launch.QuestionText}}
	// A caller trying to re-point the template at different code and a
	// different fleet.
	edit.KindName = "playbook"
	edit.Definition = "playbooks/other.yml"
	edit.InventoryID = f.invB

	if err := f.store.Update(ctx, edit); err != nil {
		t.Fatalf("Update: %v", err)
	}

	read, err := f.store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if read.Name != "patch renamed" {
		t.Errorf("the rename did not apply: %q", read.Name)
	}
	// What runs, and where, came from storage rather than the submission.
	// Re-pointing a template at different code while it keeps its name, its
	// grants and its job history is how a reviewed thing quietly becomes an
	// unreviewed one.
	if read.KindName != "runbook" || read.Definition != "patch-edge" {
		t.Errorf("an update re-pointed the template at %s/%s", read.KindName, read.Definition)
	}
	if read.InventoryID != f.invA || read.OrganizationID != f.orgA {
		t.Errorf("an update moved the template to inventory %d in tenant %d", read.InventoryID, read.OrganizationID)
	}

	// The survey was replaced wholesale rather than merged, so the removed
	// question is gone rather than lingering unanswerable.
	if len(read.Survey.Questions) != 1 || read.Survey.Questions[0].Variable != "new" {
		t.Errorf("the survey is %+v, want only the new question", read.Survey.Questions)
	}
	if n := f.client.SurveyQuestion.Query().CountX(ctx); n != 1 {
		t.Errorf("replacing a survey left %d question rows, want 1", n)
	}
}

func TestStore_DeletingATemplateTakesItsSurveyAndConfigurations(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	tmpl := f.template("patch")
	tmpl.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "v", Label: "V", Type: launch.QuestionText},
	}}
	created, err := f.store.Create(ctx, tmpl)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.store.SaveConfig(ctx, launch.SavedConfig{
		TemplateID: created.ID, Name: "nightly",
		Fields: launch.Fields{"limit": "edge-01"},
	}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	if err := f.store.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if n := f.client.SurveyQuestion.Query().CountX(ctx); n != 0 {
		t.Errorf("deleting a template left %d survey questions attached to nothing", n)
	}
	if n := f.client.SavedLaunchConfig.Query().CountX(ctx); n != 0 {
		t.Errorf("deleting a template left %d saved configurations attached to nothing", n)
	}
	if _, err := f.store.Get(ctx, created.ID); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("Get after Delete returned %v, want ErrNotFound", err)
	}
}

func TestStore_ASavedConfigurationRoundTripsAndFoldsAsALayer(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.template("patch"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	saved, err := f.store.SaveConfig(ctx, launch.SavedConfig{
		TemplateID: created.ID,
		Name:       "nightly",
		Fields:     launch.Fields{"limit": "edge-01"},
		Answers:    map[string]any{"unused": "value"},
	})
	if err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// It folds as the layer beneath a launch, which is the only reason it
	// exists: a relaunch reuses what a job ran with rather than asking
	// somebody to remember what they typed.
	resolved, ignored, err := created.Resolve(ctx, saved.Config())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("a saved configuration of an open field reported %+v", ignored)
	}
	if got := resolved.Fields.String("limit"); got != "edge-01" {
		t.Errorf("limit = %q, want the saved configuration's", got)
	}

	listed, err := f.store.SavedConfigs(ctx, created.ID)
	if err != nil {
		t.Fatalf("SavedConfigs: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "nightly" {
		t.Errorf("SavedConfigs returned %+v, want the one saved", listed)
	}
}

func TestStore_RedactsASecretAnswerOnTheWayOut(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "version", Type: launch.QuestionText},
		{Variable: "vault_token", Type: launch.QuestionPassword},
	}}
	cfg := launch.SavedConfig{Answers: map[string]any{
		"version":     "17.6",
		"vault_token": "hvs.not-a-real-token",
	}}

	redacted := cfg.Redact(survey)

	if redacted.Answers["vault_token"] != launch.RedactedMarker {
		t.Errorf("the password answer projects as %v, want the redaction marker", redacted.Answers["vault_token"])
	}
	if redacted.Answers["version"] != "17.6" {
		t.Errorf("a non-secret answer was redacted: %v", redacted.Answers["version"])
	}

	// A marker rather than an empty string, because empty and withheld are
	// different facts and a form rendering the empty string would silently
	// clear the stored answer on the next save.
	if redacted.Answers["vault_token"] == "" {
		t.Error("the password answer projects as empty, which a form would save back as a cleared value")
	}

	// The original is untouched: redaction is a projection, not a mutation,
	// and a Redact that wrote through would destroy the stored credential
	// the first time anybody read it.
	if cfg.Answers["vault_token"] != "hvs.not-a-real-token" {
		t.Error("Redact mutated the configuration it was projecting")
	}
}

func TestStore_RefusesAConfigurationBelongingToNoTemplate(t *testing.T) {
	f := newStoreFixture(t)

	if _, err := f.store.SaveConfig(context.Background(), launch.SavedConfig{Name: "orphan"}); err == nil {
		t.Error("SaveConfig accepted a configuration with no template: its fields are only meaningful against one")
	}
	if _, err := f.store.GetConfig(context.Background(), 999); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("GetConfig(missing) returned %v, want ErrNotFound", err)
	}

	// A configuration naming a template that does not exist is refused by
	// the foreign key rather than stored orphaned. Mapped to ErrNotFound so
	// a caller answers 404 for what is a reference to nothing, rather than
	// 500 for what looks like a platform failure.
	if _, err := f.store.SaveConfig(context.Background(), launch.SavedConfig{
		TemplateID: 999, Name: "orphan",
	}); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("SaveConfig naming a missing template returned %v, want ErrNotFound", err)
	}

	// And an empty listing for a template that has none is not an error:
	// "this template has no saved configurations" is an ordinary answer.
	configs, err := f.store.SavedConfigs(context.Background(), 999)
	if err != nil {
		t.Errorf("SavedConfigs for a template with none returned %v, want an empty list", err)
	}
	if len(configs) != 0 {
		t.Errorf("SavedConfigs returned %d configurations for a template that has none", len(configs))
	}
}

func TestStore_ListNarrowsPagesAndSearches(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	for _, name := range []string{"patch edge", "patch core", "reboot edge"} {
		if _, err := f.store.Create(ctx, f.template(name)); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	other := f.template("other tenant template")
	other.InventoryID = f.invB
	if _, err := f.store.Create(ctx, other); err != nil {
		t.Fatalf("Create in the second tenant: %v", err)
	}

	all, err := f.store.List(ctx, launch.Query{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("List returned %d templates, want all 4", len(all))
	}

	// An empty OrganizationIDs means no restriction, and that is the
	// caller's decision rather than this store's: authorization is the
	// admission chain's job, and a store that filtered on its own would be
	// a second place answering the same question.
	mine, err := f.store.List(ctx, launch.Query{OrganizationIDs: []int{f.orgA}})
	if err != nil {
		t.Fatalf("List(tenant): %v", err)
	}
	if len(mine) != 3 {
		t.Errorf("narrowing to one tenant returned %d templates, want 3", len(mine))
	}
	for _, tmpl := range mine {
		if tmpl.OrganizationID != f.orgA {
			t.Errorf("narrowing to tenant %d returned a template in %d", f.orgA, tmpl.OrganizationID)
		}
	}

	found, err := f.store.List(ctx, launch.Query{Search: "PATCH"})
	if err != nil {
		t.Fatalf("List(search): %v", err)
	}
	if len(found) != 2 {
		t.Errorf("a case-insensitive search for PATCH returned %d templates, want 2", len(found))
	}

	// Paging walks forward on the keyset cursor without repeating or
	// stalling.
	first, err := f.store.List(ctx, launch.Query{Limit: 2})
	if err != nil {
		t.Fatalf("List(page 1): %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("the first page holds %d templates, want 2", len(first))
	}
	second, err := f.store.List(ctx, launch.Query{Limit: 2, After: first[1].ID})
	if err != nil {
		t.Fatalf("List(page 2): %v", err)
	}
	if len(second) != 2 || second[0].ID <= first[1].ID {
		t.Errorf("the second page is %+v, want the two templates after id %d", second, first[1].ID)
	}

	// A listing does not load surveys: it renders a name, a kind and an
	// inventory, and loading every question for every row would be a query
	// per page for data no column shows.
	for _, tmpl := range all {
		if len(tmpl.Survey.Questions) != 0 {
			t.Errorf("a listing loaded template %q's survey", tmpl.Name)
		}
	}
}

func TestStore_ReportsWhatIsMissingRatherThanFailing(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	if _, err := f.store.Get(ctx, 999); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("Get(missing) returned %v, want ErrNotFound", err)
	}
	if err := f.store.Delete(ctx, 999); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("Delete(missing) returned %v, want ErrNotFound", err)
	}
	if err := f.store.Update(ctx, launch.Template{ID: 999, Name: "ghost"}); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("Update(missing) returned %v, want ErrNotFound", err)
	}

	// An inventory that does not exist is reported as such rather than as
	// a template validation failure: the caller named a real field with a
	// value that resolves to nothing, and telling them the template is
	// invalid would send them to fix the wrong thing.
	orphan := f.template("orphan")
	orphan.InventoryID = 999
	if _, err := f.store.Create(ctx, orphan); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("Create naming a missing inventory returned %v, want ErrNotFound", err)
	}

	// And no inventory at all is a template that could never run.
	none := f.template("none")
	none.InventoryID = 0
	if _, err := f.store.Create(ctx, none); !errors.Is(err, launch.ErrInvalidTemplate) {
		t.Errorf("Create naming no inventory returned %v, want ErrInvalidTemplate", err)
	}
}

func TestStore_RefusesATemplateItsOwnKindWouldReject(t *testing.T) {
	f := newStoreFixture(t)

	// The kind's own ValidateDefinition runs at the write, so a reference
	// that could never resolve to anything safe is refused before it is
	// stored rather than at the moment somebody tries to run it.
	bad := f.template("traversal")
	bad.Definition = "../../etc/passwd"
	if _, err := f.store.Create(context.Background(), bad); !errors.Is(err, launch.ErrInvalidTemplate) {
		t.Errorf("Create with a traversing definition returned %v, want ErrInvalidTemplate", err)
	}
}

func TestStore_UpdateRefusesARenameOntoATakenName(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	first, err := f.store.Create(ctx, f.template("patch"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.store.Create(ctx, f.template("reboot")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Renaming onto a name already taken in the same tenant is the
	// caller's mistake, which is what makes it a 409 rather than a 500.
	rename := first
	rename.Name = "reboot"
	if err := f.store.Update(ctx, rename); !errors.Is(err, launch.ErrExists) {
		t.Errorf("Update onto a taken name returned %v, want ErrExists", err)
	}

	// An edit that would make the template unlaunchable is refused before
	// it is stored, for the reason Validate's own comment gives: a record
	// that cannot be used is one somebody finds out about at the moment
	// they are trying to run it.
	broken := first
	broken.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "version", Type: launch.QuestionChoice}, // a choice between nothing
	}}
	if err := f.store.Update(ctx, broken); !errors.Is(err, launch.ErrInvalidSurvey) {
		t.Errorf("Update with an unanswerable survey returned %v, want ErrInvalidSurvey", err)
	}

	// And the stored record is untouched by either refusal.
	read, err := f.store.Get(ctx, first.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if read.Name != "patch" || read.Survey.Enabled {
		t.Errorf("a refused update changed the stored template: %+v", read)
	}
}

// TestCreate_RefusesADefinitionTheDeploymentCannotLaunch is the
// create-time existence gate. Shape validation cannot catch this
// ("no-such-runbook" is a perfectly shaped id), and before the catalog
// check existed the row was saved, the launch answered 202, and the
// mistake surfaced three stages later as a failed job at fan-out.
func TestCreate_RefusesADefinitionTheDeploymentCannotLaunch(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	tmpl := f.template("ghost")
	tmpl.Definition = "no-such-runbook"
	_, err := f.store.Create(ctx, tmpl)
	if !errors.Is(err, launch.ErrDefinitionNotFound) {
		t.Fatalf("Create with an unresolvable definition = %v, want ErrDefinitionNotFound", err)
	}

	// And nothing was persisted: a refusal that left the row behind would
	// be the old failure with an error message stapled on.
	if templates, listErr := f.store.List(ctx, launch.Query{Limit: 50}); listErr == nil {
		for _, saved := range templates {
			if saved.Name == "ghost" {
				t.Error("the refused template was persisted anyway")
			}
		}
	}
}
