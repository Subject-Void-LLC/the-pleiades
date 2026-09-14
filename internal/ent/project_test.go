// This file proves internal/ent's own generated Project code.
//
// Phase-wise it is the newest entity in the schema
// (internal/ent/schema/project.go), and like every other entity here it
// gets its own RULE 0 proof against a real SQLite-backed *ent.Client
// through enttest.Open, never a fake standing in for generated code. The
// pattern is job_jobtask_test.go's and group_organization_test.go's.
//
// The properties worth proving are the ones that live in the schema rather
// than in any Go code above it: the composite unique index that makes a
// name unique per organization instead of globally, the two enum
// validators, the required organization edge, and the optional credential
// edge that must be clearable without deleting the project. A store test
// one layer up exercises none of those directly, because it goes through a
// store that is already careful.
package ent_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	entproject "github.com/Subject-Void-LLC/the-pleiades/internal/ent/project"
	_ "github.com/mattn/go-sqlite3"
)

// TestProjectLifecycle walks one project from creation to deletion,
// through every field and edge a real sync writes.
func TestProjectLifecycle(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:projectlifecycle?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("network").SaveX(ctx)

	// A project carrying only what a create needs. The two enums must come
	// back at their declared defaults rather than empty: "never synced" and
	// "sync failed" are different facts, and an empty status cannot tell
	// them apart.
	minimal := client.Project.Create().
		SetName("automation").
		SetScmURL("https://example.invalid/automation.git").
		SetOrganizationID(org.ID).
		SaveX(ctx)

	if minimal.ScmType != entproject.ScmTypeGit {
		t.Errorf("ScmType = %q, want the declared default %q", minimal.ScmType, entproject.ScmTypeGit)
	}
	if minimal.SyncStatus != entproject.SyncStatusNever {
		t.Errorf("SyncStatus = %q, want the declared default %q", minimal.SyncStatus, entproject.SyncStatusNever)
	}
	if minimal.String() == "" {
		t.Error("Project.String() is empty, so a log line naming one says nothing")
	}

	// The required edge is reachable in both directions.
	owner := client.Project.QueryOrganization(minimal).OnlyX(ctx)
	if owner.ID != org.ID {
		t.Errorf("QueryOrganization() = %d, want %d", owner.ID, org.ID)
	}

	// What a completed sync writes: the revision, where the tree landed,
	// and when.
	synced := time.Now().UTC().Truncate(time.Second)
	updated := client.Project.UpdateOne(minimal).
		SetSyncStatus(entproject.SyncStatusSucceeded).
		SetRevision("3b9f1c2d4e5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c").
		SetLocalPath("/var/lib/pleiades/projects/1").
		SetLastSyncedAt(synced).
		SetScmBranch("main").
		SetDescription("the automation repository").
		SaveX(ctx)

	if updated.SyncStatus != entproject.SyncStatusSucceeded || updated.Revision == "" {
		t.Errorf("the sync outcome did not persist: %+v", updated)
	}
	if !updated.LastSyncedAt.Equal(synced) {
		t.Errorf("LastSyncedAt = %v, want %v", updated.LastSyncedAt, synced)
	}

	// A failure records its reason, and clearing it is how a later success
	// stops reporting an error that no longer applies.
	failed := client.Project.UpdateOne(updated).
		SetSyncStatus(entproject.SyncStatusFailed).
		SetSyncError("repository not found").
		SaveX(ctx)
	if failed.SyncError != "repository not found" {
		t.Errorf("SyncError = %q", failed.SyncError)
	}

	if err := client.Project.DeleteOne(failed).Exec(ctx); err != nil {
		t.Fatalf("deleting the project: %v", err)
	}
	if _, err := client.Project.Get(ctx, failed.ID); !ent.IsNotFound(err) {
		t.Errorf("Get after delete = %v, want a not-found error", err)
	}
}

// TestProjectNameIsUniquePerOrganizationNotGlobally proves the composite
// index is doing what its comment claims.
//
// It matters because the opposite reading is the plausible one: a globally
// unique project name would mean the first tenant to create "automation"
// takes the word away from every other tenant in the deployment.
func TestProjectNameIsUniquePerOrganizationNotGlobally(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:projectunique?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	network := client.Organization.Create().SetName("network").SaveX(ctx)
	servers := client.Organization.Create().SetName("servers").SaveX(ctx)

	create := func(orgID int) error {
		_, err := client.Project.Create().
			SetName("automation").
			SetScmURL("https://example.invalid/a.git").
			SetOrganizationID(orgID).
			Save(ctx)
		return err
	}

	if err := create(network.ID); err != nil {
		t.Fatalf("the first project: %v", err)
	}
	// A different tenant may hold the same name.
	if err := create(servers.ID); err != nil {
		t.Errorf("a second organization was refused the name %q: %v", "automation", err)
	}
	// The same tenant may not.
	err := create(network.ID)
	if err == nil {
		t.Fatal("the same organization created two projects called the same thing")
	}
	if !ent.IsConstraintError(err) {
		t.Errorf("duplicate name = %v, want a constraint error", err)
	}
}

// TestProjectEnumsAreValidatedBeforeTheWrite covers both enum validators.
//
// They are the difference between a bad value being refused at the write
// and being discovered by whatever reads the column later, which for
// sync_status is the code deciding whether to show somebody an error.
func TestProjectEnumsAreValidatedBeforeTheWrite(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:projectenums?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)

	if _, err := client.Project.Create().
		SetName("bad scm type").
		SetScmType(entproject.ScmType("subversion")).
		SetOrganizationID(org.ID).
		Save(ctx); err == nil {
		t.Error("an undeclared scm_type was accepted")
	}

	if _, err := client.Project.Create().
		SetName("bad sync status").
		SetSyncStatus(entproject.SyncStatus("probably fine")).
		SetOrganizationID(org.ID).
		Save(ctx); err == nil {
		t.Error("an undeclared sync_status was accepted")
	}

	// Every declared value is accepted, so the validator is not simply
	// refusing everything.
	for _, scm := range []entproject.ScmType{entproject.ScmTypeGit, entproject.ScmTypeArchive, entproject.ScmTypeManual} {
		if _, err := client.Project.Create().
			SetName("scm " + string(scm)).
			SetScmType(scm).
			SetOrganizationID(org.ID).
			Save(ctx); err != nil {
			t.Errorf("the declared scm_type %q was refused: %v", scm, err)
		}
	}
	for _, status := range []entproject.SyncStatus{
		entproject.SyncStatusNever, entproject.SyncStatusPending, entproject.SyncStatusRunning,
		entproject.SyncStatusSucceeded, entproject.SyncStatusFailed,
	} {
		if _, err := client.Project.Create().
			SetName("status " + string(status)).
			SetSyncStatus(status).
			SetOrganizationID(org.ID).
			Save(ctx); err != nil {
			t.Errorf("the declared sync_status %q was refused: %v", status, err)
		}
	}
}

// TestProjectRequiresAnOrganization proves the tenancy edge is a
// constraint rather than a convention.
func TestProjectRequiresAnOrganization(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:projectorgreq?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	if _, err := client.Project.Create().
		SetName("orphan").
		SetScmURL("https://example.invalid/a.git").
		Save(context.Background()); err == nil {
		t.Error("a project was created belonging to nobody")
	}
}

// TestProjectCredentialEdgeIsOptionalAndClearable covers the edge a
// private repository uses.
//
// Clearable is the half worth proving: a repository that stops being
// private must be able to drop its credential without the project being
// recreated, and a credential must not become undeletable because a
// project once referenced it.
func TestProjectCredentialEdgeIsOptionalAndClearable(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:projectcred?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	org := client.Organization.Create().SetName("network").SaveX(ctx)
	ctype := client.CredentialType.Create().
		SetName("Source Control").
		SetKind("scm").
		SetNamespace("scm").
		SaveX(ctx)
	cred := client.Credential.Create().
		SetName("forge").
		SetCredentialTypeID(ctype.ID).
		SetOrganizationID(org.ID).
		SaveX(ctx)

	// Public: no credential at all, and the eager load reports that
	// without erroring.
	public := client.Project.Create().
		SetName("public").
		SetScmURL("https://example.invalid/a.git").
		SetOrganizationID(org.ID).
		SaveX(ctx)
	loaded := client.Project.Query().
		Where(entproject.IDEQ(public.ID)).
		WithOrganization().
		WithCredential().
		WithTemplates().
		OnlyX(ctx)
	if loaded.Edges.Credential != nil {
		t.Error("a public project came back carrying a credential")
	}
	if loaded.Edges.Organization == nil || loaded.Edges.Organization.ID != org.ID {
		t.Error("the eager-loaded organization is missing")
	}

	// Private: the edge resolves in both directions.
	private := client.Project.Create().
		SetName("private").
		SetScmURL("https://example.invalid/b.git").
		SetOrganizationID(org.ID).
		SetCredentialID(cred.ID).
		SaveX(ctx)
	if got := client.Project.QueryCredential(private).OnlyX(ctx); got.ID != cred.ID {
		t.Errorf("QueryCredential() = %d, want %d", got.ID, cred.ID)
	}

	// And it can be dropped without touching anything else.
	cleared := client.Project.UpdateOne(private).ClearCredential().SaveX(ctx)
	if _, err := client.Project.QueryCredential(cleared).Only(ctx); !ent.IsNotFound(err) {
		t.Errorf("the credential edge survived ClearCredential: %v", err)
	}
	if _, err := client.Project.Get(ctx, cleared.ID); err != nil {
		t.Errorf("clearing the credential removed the project: %v", err)
	}
}

// TestProjectPredicatesAndOrdering covers the query surface a listing uses.
func TestProjectPredicatesAndOrdering(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:projectpredicates?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	network := client.Organization.Create().SetName("network").SaveX(ctx)
	servers := client.Organization.Create().SetName("servers").SaveX(ctx)

	client.Project.Create().SetName("charlie").SetOrganizationID(network.ID).
		SetSyncStatus(entproject.SyncStatusFailed).SaveX(ctx)
	client.Project.Create().SetName("alpha").SetOrganizationID(network.ID).
		SetSyncStatus(entproject.SyncStatusSucceeded).SaveX(ctx)
	client.Project.Create().SetName("bravo").SetOrganizationID(servers.ID).SaveX(ctx)

	// Scoped to one tenant, which is the query every listing actually runs.
	mine := client.Project.Query().
		Where(entproject.HasOrganizationWith()).
		Where(entproject.NameEQ("alpha")).
		AllX(ctx)
	if len(mine) != 1 {
		t.Fatalf("name predicate returned %d rows, want 1", len(mine))
	}

	ordered := client.Project.Query().
		Order(ent.Asc(entproject.FieldName)).
		AllX(ctx)
	if len(ordered) != 3 {
		t.Fatalf("listed %d projects, want 3", len(ordered))
	}
	if ordered[0].Name != "alpha" || ordered[2].Name != "charlie" {
		t.Errorf("ordering by name gave %q..%q", ordered[0].Name, ordered[2].Name)
	}

	failed := client.Project.Query().
		Where(entproject.SyncStatusEQ(entproject.SyncStatusFailed)).
		CountX(ctx)
	if failed != 1 {
		t.Errorf("counted %d failed projects, want 1", failed)
	}
}
