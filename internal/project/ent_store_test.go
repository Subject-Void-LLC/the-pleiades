// This file covers the two store operations the asynchronous sync path
// added: BeginSync, the compare-and-swap that claims a project so two Syncs
// cannot both clone into one working tree, and ResetInterruptedSyncs, the
// startup sweep that clears a claim a process restart left behind.
package project_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"

	_ "github.com/mattn/go-sqlite3"
)

// syncableProject creates one project with a fetchable git source and
// returns its id, plus the store it lives in.
func syncableProject(t *testing.T) (project.Store, int) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s/projects.db?_fk=1", t.TempDir())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	org := client.Organization.Create().SetName("network").SaveX(ctx)
	store := project.NewEntStore(client)

	p, err := store.Create(ctx, project.Project{
		Name:           "repo",
		SCMType:        project.SCMGit,
		SCMURL:         "https://example.invalid/a.git",
		OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("creating a project: %v", err)
	}
	return store, p.ID
}

func TestBeginSync_ClaimsThenRefusesASecondClaim(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	claimed, err := store.BeginSync(ctx, id)
	if err != nil {
		t.Fatalf("BeginSync() = %v, want it to claim a fresh project", err)
	}
	if claimed.SyncStatus != project.SyncRunning {
		t.Errorf("claimed project status = %q, want running", claimed.SyncStatus)
	}

	// The row itself is now running, so a reader sees the claim.
	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if got.SyncStatus != project.SyncRunning {
		t.Errorf("stored status after a claim = %q, want running", got.SyncStatus)
	}

	// A second claim is refused rather than starting a second clone.
	if _, err := store.BeginSync(ctx, id); !errors.Is(err, project.ErrSyncInProgress) {
		t.Errorf("second BeginSync() = %v, want ErrSyncInProgress", err)
	}
}

func TestBeginSync_RefusesTheUnsyncableAndTheUnknown(t *testing.T) {
	store, _ := syncableProject(t)
	ctx := context.Background()

	if _, err := store.BeginSync(ctx, 999999); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("BeginSync of an unknown id = %v, want ErrNotFound", err)
	}

	// A project with no fetchable source is refused synchronously rather
	// than claimed and failed in the background.
	unsyncable, err := store.Create(ctx, project.Project{
		Name:    "manual-only",
		SCMType: project.SCMManual,
		// A manual project has no URL to fetch.
		OrganizationID: mustOrgID(t, store, ctx),
	})
	if err != nil {
		t.Fatalf("creating an unsyncable project: %v", err)
	}
	if _, err := store.BeginSync(ctx, unsyncable.ID); !errors.Is(err, project.ErrNotSyncable) {
		t.Errorf("BeginSync of an unsyncable project = %v, want ErrNotSyncable", err)
	}
}

func TestResetInterruptedSyncs_ClearsARunningClaim(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	if _, err := store.BeginSync(ctx, id); err != nil {
		t.Fatalf("BeginSync() = %v", err)
	}

	n, err := store.ResetInterruptedSyncs(ctx)
	if err != nil {
		t.Fatalf("ResetInterruptedSyncs() = %v", err)
	}
	if n != 1 {
		t.Errorf("reset %d syncs, want 1", n)
	}

	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if got.SyncStatus != project.SyncFailed {
		t.Errorf("status after reset = %q, want failed", got.SyncStatus)
	}
	if got.SyncError == "" {
		t.Error("a reset sync carries no explanation, so a reader cannot tell it apart from a real failure")
	}

	// With the claim cleared, the project can be synced again: the swap
	// that a stuck running row would have blocked forever now matches.
	if _, err := store.BeginSync(ctx, id); err != nil {
		t.Errorf("BeginSync after a reset = %v, want the claim to succeed again", err)
	}
}

// mustOrgID reads an organization id out of the store's single project, so a
// second project in the same test shares the tenant the first one made.
func mustOrgID(t *testing.T, store project.Store, ctx context.Context) int {
	t.Helper()
	ps, err := store.List(ctx, project.Query{Limit: 1})
	if err != nil || len(ps) == 0 {
		t.Fatalf("List() = %v (%d rows), want the seeded project", err, len(ps))
	}
	return ps[0].OrganizationID
}
