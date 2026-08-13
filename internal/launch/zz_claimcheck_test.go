package launch_test

// Temporary verification test for a code-review claim. DELETE AFTER RUNNING.

import (
	"context"
	"testing"
)

// TestClaim_OldGrammarRowFailsUpdate simulates a row persisted by the
// previous commit's grammar (playbook definitions were .yml/.yaml paths;
// runbook ids allowed up to 253 chars) by inserting it directly through the
// ent client, then attempts an Update that changes only the description.
func TestClaim_OldGrammarRowFailsUpdate(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	// A playbook row exactly as the old validatePlaybookPath would have
	// accepted it ("site.yml"), written directly to storage.
	row := f.client.Template.Create().
		SetName("legacy-playbook").
		SetDescription("created under the old grammar").
		SetKind("playbook").
		SetDefinition("site.yml").
		SetOrganizationID(f.orgA).
		SetInventoryID(f.invA).
		SaveX(ctx)

	got, err := f.store.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The caller edits nothing but the description.
	got.Description = "only the description changed"
	if err := f.store.Update(ctx, got); err != nil {
		t.Logf("PLAYBOOK UPDATE ERROR: %v", err)
	} else {
		t.Logf("PLAYBOOK UPDATE: succeeded")
	}

	// Same shape for a runbook whose id was legal at 65-253 chars.
	longID := make([]byte, 80)
	for i := range longID {
		longID[i] = 'a'
	}
	row2 := f.client.Template.Create().
		SetName("legacy-runbook").
		SetDescription("created under the old 253-char bound").
		SetKind("runbook").
		SetDefinition(string(longID)).
		SetOrganizationID(f.orgA).
		SetInventoryID(f.invA).
		SaveX(ctx)

	got2, err := f.store.Get(ctx, row2.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got2.Description = "only the description changed"
	if err := f.store.Update(ctx, got2); err != nil {
		t.Logf("RUNBOOK UPDATE ERROR: %v", err)
	} else {
		t.Logf("RUNBOOK UPDATE: succeeded")
	}
}
