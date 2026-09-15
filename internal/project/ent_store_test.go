// This file covers the store operations the asynchronous sync path added:
// BeginSync, the compare-and-swap that claims a project so two Syncs cannot
// both clone into one working tree; ResetInterruptedSyncs, the startup sweep
// that clears a claim a process restart left behind; and the sync history a
// completed attempt appends, which is separate from the latest outcome the
// project row itself keeps.
package project_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

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

// TestRecordSync_AppendsToTheHistory proves each completed attempt becomes a
// row of its own, newest first, while the project keeps the latest outcome.
func TestRecordSync_AppendsToTheHistory(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	failedStart := time.Now().Add(-time.Minute)
	if err := store.RecordSync(ctx, id, project.Result{
		Status:    project.SyncFailed,
		Err:       "host unreachable",
		StartedAt: failedStart,
		At:        failedStart.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("recording the failed attempt: %v", err)
	}

	okStart := time.Now()
	if err := store.RecordSync(ctx, id, project.Result{
		Status:    project.SyncSucceeded,
		Revision:  "abc123",
		LocalPath: "/var/lib/pleiades/projects/1",
		StartedAt: okStart,
		At:        okStart.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("recording the successful attempt: %v", err)
	}

	runs, err := store.ListSyncRuns(ctx, id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("recorded %d runs, want both attempts", len(runs))
	}
	if runs[0].Status != project.SyncSucceeded || runs[0].Revision != "abc123" {
		t.Errorf("newest run = %+v, want the succeeded attempt first", runs[0])
	}
	if runs[1].Status != project.SyncFailed || runs[1].Err != "host unreachable" {
		t.Errorf("older run = %+v, want the failed attempt with its reason", runs[1])
	}
	if runs[0].Took() <= 0 {
		t.Errorf("a run reports no duration (%v), so a history cannot say how long it took", runs[0].Took())
	}

	// The project itself still carries the LATEST outcome, which is what a
	// badge and a playbook lookup read rather than sorting this history.
	p, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if p.SyncStatus != project.SyncSucceeded || p.Revision != "abc123" {
		t.Errorf("project latest = %q/%q, want the succeeded attempt", p.SyncStatus, p.Revision)
	}
}

// TestListSyncRuns_RecordsOnlyTerminalAttempts proves a running claim is not
// also a history row: the project's own status is where an in-flight attempt
// shows, and duplicating it here would strand a row on every crash.
func TestListSyncRuns_RecordsOnlyTerminalAttempts(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	if _, err := store.BeginSync(ctx, id); err != nil {
		t.Fatalf("BeginSync() = %v", err)
	}
	runs, err := store.ListSyncRuns(ctx, id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("a claimed but unfinished sync wrote %d history rows, want none", len(runs))
	}
}
