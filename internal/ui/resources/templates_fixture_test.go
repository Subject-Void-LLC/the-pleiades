package resources_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// newTestTemplateStore is the Templates view's port, ent-backed and seeded,
// for the same reason newTestAccessStore is: a fake returning empty slices
// gives the view nothing to render, and every assertion about columns,
// badges and detail sections would then be an assertion about a blank page.
//
// It opens the same in-memory database the access fixture seeds, so a
// template's organization edge resolves to an organization that exists.
// Tenancy is derived from the inventory rather than submitted, so without a
// real inventory under a real organization the store would refuse every
// template this suite tries to create.
func newTestTemplateStore(t *testing.T) launch.Store {
	t.Helper()

	// No t.Cleanup, for the reason the access fixture states: the view
	// registry is process-wide and the ports it captures outlive whichever
	// test triggered the registration.
	client := enttest.Open(t, "sqlite3", "file:uiaccessfixture?mode=memory&cache=shared&_fk=1")
	ctx := context.Background()

	org, err := client.Organization.Query().First(ctx)
	if err != nil {
		t.Fatalf("reading the seeded organization: %v", err)
	}
	set, err := client.Inventory.Create().
		SetName("conformance-inventory").
		SetOrganization(org).
		Save(ctx)
	if err != nil {
		t.Fatalf("seeding an inventory: %v", err)
	}

	store := launch.NewEntStore(client, launch.StaticCatalog(
		launch.CatalogEntry{Kind: "runbook", Definition: "conformance"},
		launch.CatalogEntry{Kind: "playbook", Definition: "tripplite_python/tripplite_config.yml"},
	))

	// Two templates, and the pair is the point. One opens two fields and
	// asks a survey question, so the launch form has controls to render and
	// something to leave out; the other opens nothing, so the same form is
	// a confirmation. A suite with only the first would never notice a
	// launch form that rendered every field regardless.
	if _, err := store.Create(ctx, launch.Template{
		Name:        "conformance-template",
		Description: "A template the conformance suite renders.",
		KindName:    "runbook",
		Definition:  "conformance",
		InventoryID: set.ID,
		Defaults:    launch.Fields{"limit": "edge-*", "forks": 5, "verbosity": 1},
		Prompts:     []string{"limit", "forks"},
		Survey: launch.Survey{Enabled: true, Questions: []launch.Question{
			{Variable: "version", Label: "Version", Type: launch.QuestionChoice, Required: true, Choices: []string{"17.3", "17.6"}},
			{Variable: "vault_token", Label: "Vault token", Type: launch.QuestionPassword},
		}},
	}); err != nil {
		t.Fatalf("seeding a template: %v", err)
	}

	if _, err := store.Create(ctx, launch.Template{
		Name:        "conformance-locked",
		KindName:    "playbook",
		Definition:  "tripplite_python/tripplite_config.yml",
		InventoryID: set.ID,
		Defaults:    launch.Fields{"limit": "all"},
	}); err != nil {
		t.Fatalf("seeding a locked template: %v", err)
	}

	return store
}
