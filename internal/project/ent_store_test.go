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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"

	_ "github.com/mattn/go-sqlite3"
)

// syncableProject creates one project with a fetchable git source and
// returns its id, plus the store it lives in.
// newStoreClient opens a real database for a test that needs to build more
// than one store over it, which the source-policy control does: it writes
// through a store that permits local paths and syncs with one that does not.
func newStoreClient(t *testing.T) *ent.Client {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s/projects.db?_fk=1", t.TempDir()))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func syncableProject(t *testing.T) (project.Store, int) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s/projects.db?_fk=1", t.TempDir())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	org := client.Organization.Create().SetName("network").SaveX(ctx)
	// The default policy: every fixture here names an https source, so this
	// suite exercises the ordinary case. The two tests that need a local path
	// build their own store and say so.
	store := project.NewEntStore(client, project.SourcePolicy{})

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

	claim, err := store.BeginSync(ctx, id, "tester")
	if err != nil {
		t.Fatalf("BeginSync() = %v, want it to claim a fresh project", err)
	}
	if claim.Project.SyncStatus != project.SyncRunning {
		t.Errorf("claimed project status = %q, want running", claim.Project.SyncStatus)
	}
	if claim.RunID == 0 {
		t.Error("the claim names no attempt, so nothing that started it can say which one it started")
	}
	if claim.StartedAt.IsZero() {
		t.Error("the claim has no start time, so the runner and the history row would disagree about when it began")
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
	if _, err := store.BeginSync(ctx, id, "tester"); !errors.Is(err, project.ErrSyncInProgress) {
		t.Errorf("second BeginSync() = %v, want ErrSyncInProgress", err)
	}

	// The claim opened exactly one history row, and it reads as running with
	// the actor that asked for it.
	runs, err := store.ListSyncRuns(ctx, id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("a claim wrote %d history rows, want exactly 1", len(runs))
	}
	if runs[0].ID != claim.RunID {
		t.Errorf("history row id = %d, want the claim's %d", runs[0].ID, claim.RunID)
	}
	if runs[0].Status != project.SyncRunning || !runs[0].Running() {
		t.Errorf("claimed attempt = %+v, want it running with no finish time", runs[0])
	}
	if runs[0].Actor != "tester" {
		t.Errorf("attempt actor = %q, want the actor the claim carried", runs[0].Actor)
	}
}

// TestBeginSync_RefusesAClaimWithNoActor proves a sync nobody can be held to
// is refused rather than recorded with an invented name.
func TestBeginSync_RefusesAClaimWithNoActor(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	if _, err := store.BeginSync(ctx, id, "   "); !errors.Is(err, project.ErrNoActor) {
		t.Errorf("BeginSync with a blank actor = %v, want ErrNoActor", err)
	}

	// Refused means nothing moved: neither the project nor a history row.
	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}
	if got.SyncStatus == project.SyncRunning {
		t.Error("a refused claim still moved the project to running")
	}
	runs, err := store.ListSyncRuns(ctx, id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("a refused claim wrote %d history rows, want none", len(runs))
	}
}

func TestBeginSync_RefusesTheUnsyncableAndTheUnknown(t *testing.T) {
	store, _ := syncableProject(t)
	ctx := context.Background()

	if _, err := store.BeginSync(ctx, 999999, "tester"); !errors.Is(err, project.ErrNotFound) {
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
	if _, err := store.BeginSync(ctx, unsyncable.ID, "tester"); !errors.Is(err, project.ErrNotSyncable) {
		t.Errorf("BeginSync of an unsyncable project = %v, want ErrNotSyncable", err)
	}
}

func TestResetInterruptedSyncs_ClearsARunningClaim(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	claim, err := store.BeginSync(ctx, id, "tester")
	if err != nil {
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

	// The attempt's own row is failed too, rather than left cloning forever
	// on a page whose duration would keep counting up.
	runs, err := store.ListSyncRuns(ctx, id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != claim.RunID {
		t.Fatalf("history = %+v, want the claimed attempt", runs)
	}
	if runs[0].Status != project.SyncFailed || runs[0].Running() {
		t.Errorf("interrupted attempt = %+v, want it failed with a finish time", runs[0])
	}
	if runs[0].Err == "" {
		t.Error("an interrupted attempt carries no explanation")
	}

	// With the claim cleared, the project can be synced again: the swap
	// that a stuck running row would have blocked forever now matches.
	if _, err := store.BeginSync(ctx, id, "tester"); err != nil {
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

// TestRecordSync_FinishesTheClaimedAttemptInPlace proves an attempt keeps the
// identity its claim gave it: the outcome closes that row rather than
// appending a second one, so anything holding the run id still points at the
// attempt it started.
func TestRecordSync_FinishesTheClaimedAttemptInPlace(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	claim, err := store.BeginSync(ctx, id, "tester")
	if err != nil {
		t.Fatalf("BeginSync() = %v", err)
	}

	if err := store.RecordSync(ctx, id, project.Result{
		Status:    project.SyncSucceeded,
		Revision:  "abc123",
		LocalPath: "/var/lib/pleiades/projects/1",
		StartedAt: claim.StartedAt,
		At:        claim.StartedAt.Add(2 * time.Second),
		RunID:     claim.RunID,
	}); err != nil {
		t.Fatalf("RecordSync() = %v", err)
	}

	runs, err := store.ListSyncRuns(ctx, id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("finishing a claimed attempt left %d rows, want the one the claim opened", len(runs))
	}
	got := runs[0]
	if got.ID != claim.RunID {
		t.Errorf("history row id = %d, want the claim's %d", got.ID, claim.RunID)
	}
	if got.Status != project.SyncSucceeded || got.Revision != "abc123" {
		t.Errorf("finished attempt = %+v, want the succeeded outcome", got)
	}
	if got.Running() {
		t.Error("a finished attempt still reads as running")
	}
	if got.Actor != "tester" {
		t.Errorf("actor after finishing = %q, want it kept from the claim", got.Actor)
	}
	if got.Took() != 2*time.Second {
		t.Errorf("Took() = %v, want the 2s between the claim and the outcome", got.Took())
	}
}

// TestRecordSync_RefusesARunFromAnotherProject proves the run id is checked
// against the project it is recorded for, so a stale or forged pair closes
// nothing instead of stamping an outcome onto another project's history.
func TestRecordSync_RefusesARunFromAnotherProject(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	other, err := store.Create(ctx, project.Project{
		Name:           "other",
		SCMType:        project.SCMGit,
		SCMURL:         "https://example.invalid/other.git",
		OrganizationID: mustOrgID(t, store, ctx),
	})
	if err != nil {
		t.Fatalf("creating a second project: %v", err)
	}
	claim, err := store.BeginSync(ctx, other.ID, "tester")
	if err != nil {
		t.Fatalf("BeginSync() = %v", err)
	}

	err = store.RecordSync(ctx, id, project.Result{
		Status: project.SyncSucceeded,
		At:     time.Now(),
		RunID:  claim.RunID,
	})
	if err == nil {
		t.Fatal("recording another project's attempt was accepted")
	}

	// The other project's attempt is untouched: still running, still its own.
	runs, err := store.ListSyncRuns(ctx, other.ID, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() = %v", err)
	}
	if len(runs) != 1 || !runs[0].Running() {
		t.Errorf("other project's history = %+v, want its attempt still running", runs)
	}
}

// TestDelete_TakesTheSyncHistoryWithIt proves a project that has synced can
// still be deleted, which it could not before: the history's foreign key was
// declared without a cascade, so the delete failed on the very projects most
// likely to be deleted, and the schema's own comment said the opposite
// (FAILURE_PATTERNS.md #267).
func TestDelete_TakesTheSyncHistoryWithIt(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	claim, err := store.BeginSync(ctx, id, "tester")
	if err != nil {
		t.Fatalf("BeginSync() = %v", err)
	}
	if err := store.RecordSync(ctx, id, project.Result{
		Status:   project.SyncSucceeded,
		Revision: "abc123",
		At:       time.Now(),
		RunID:    claim.RunID,
	}); err != nil {
		t.Fatalf("RecordSync() = %v", err)
	}

	if err := store.Delete(ctx, id); err != nil {
		t.Fatalf("Delete() of a project with sync history = %v, want it to succeed", err)
	}

	if _, err := store.Get(ctx, id); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("Get() after Delete() = %v, want ErrNotFound", err)
	}
	runs, err := store.ListSyncRuns(ctx, id, 0)
	if err != nil {
		t.Fatalf("ListSyncRuns() after Delete() = %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("%d history rows outlived their project", len(runs))
	}
}

// TestCreate_WritesTheLaunchableRowThatMakesASyncSchedulable is the project
// half of what makes a sync schedulable at all: a schedule points at the
// launchable row standing for the project, so a project created without one
// could never be put on a timer.
func TestCreate_WritesTheLaunchableRowThatMakesASyncSchedulable(t *testing.T) {
	store, id := syncableProject(t)
	ctx := context.Background()

	p, err := store.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.LaunchableID == 0 {
		t.Fatal("the project reports no launchable row, so nothing could schedule its sync")
	}

	// Resolvable back the way a launch resolves it, which is the only way a
	// fired schedule reaches the project again.
	back, err := store.ByLaunchable(ctx, p.LaunchableID)
	if err != nil {
		t.Fatalf("ByLaunchable: %v", err)
	}
	if back.ID != id {
		t.Errorf("launchable %d resolves to project %d, want %d", p.LaunchableID, back.ID, id)
	}

	// A rename reaches the row, so a schedule picker never offers a name this
	// project no longer has.
	p.Name = "renamed-automation"
	if err := store.Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}
	renamed, err := store.ByLaunchable(ctx, p.LaunchableID)
	if err != nil {
		t.Fatalf("ByLaunchable after a rename: %v", err)
	}
	if renamed.Name != "renamed-automation" {
		t.Errorf("project name after a rename = %q, want the new name", renamed.Name)
	}

	// And an unknown reference is ErrNotFound rather than a zero project,
	// which is what lets a launcher refuse rather than sync the wrong thing.
	if _, err := store.ByLaunchable(ctx, 999999); !errors.Is(err, project.ErrNotFound) {
		t.Errorf("ByLaunchable of an unknown id = %v, want ErrNotFound", err)
	}
}

// TestStore_RefusesASourceTheDeploymentWillNotFetchFrom covers the write half
// of the source rule at the one place every writer passes through.
//
// The store rather than a handler, deliberately: the API, the UI, every test
// and any future importer all reach the column through Create and Update, so a
// check here is a check they all get, and a fourth caller added later is safe
// without knowing the rule exists.
func TestStore_RefusesASourceTheDeploymentWillNotFetchFrom(t *testing.T) {
	store, _ := syncableProject(t)
	ctx := context.Background()
	orgID := mustOrgID(t, store, ctx)

	refused := []struct {
		name string
		url  string
	}{
		{"a path on this server", "/srv/repos/automation.git"},
		{"an explicit file url", "file:///srv/repos/automation.git"},
		{"plain http", "http://mirror.internal/org/repo.git"},
		{"the git daemon", "git://git.internal/org/repo.git"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Create(ctx, project.Project{
				Name: "refused-" + tc.name, SCMType: project.SCMGit,
				SCMURL: tc.url, OrganizationID: orgID,
			})
			if !errors.Is(err, project.ErrSourceRefused) {
				t.Fatalf("Create with %q = %v, want ErrSourceRefused", tc.url, err)
			}
		})
	}

	// A password in the URL is refused separately, because the column is not
	// encrypted and a credential is. The message points at the credential.
	_, err := store.Create(ctx, project.Project{
		Name: "token-in-url", SCMType: project.SCMGit,
		SCMURL: "https://someone:ghp-TOKEN@github.com/org/repo.git", OrganizationID: orgID,
	})
	if !errors.Is(err, project.ErrSourceSecretInURL) {
		t.Fatalf("Create with a password in the URL = %v, want ErrSourceSecretInURL", err)
	}

	// An edit is checked too, so a row cannot be repointed at a refused source
	// after the fact.
	existing, err := store.Create(ctx, project.Project{
		Name: "ordinary", SCMType: project.SCMGit,
		SCMURL: "https://github.com/org/repo.git", OrganizationID: orgID,
	})
	if err != nil {
		t.Fatalf("Create with an https source = %v, want it accepted", err)
	}
	existing.SCMURL = "/srv/repos/automation.git"
	if err := store.Update(ctx, existing); !errors.Is(err, project.ErrSourceRefused) {
		t.Errorf("Update repointing at a local path = %v, want ErrSourceRefused", err)
	}

	// A project of a type nothing fetches keeps its value, because only a git
	// project is ever dialed. The predicate itself does not look at the type,
	// so the day archive ships its URL gets the same treatment.
	if _, err := store.Create(ctx, project.Project{
		Name: "hand-managed", SCMType: project.SCMManual,
		SCMURL: "/srv/somewhere", OrganizationID: orgID,
	}); err != nil {
		t.Errorf("Create of a manual project with a local path = %v, want it accepted", err)
	}
}

// TestStore_ASourceAcceptedAtTheWriteIsStillCheckedAtTheSync is the control
// that proves the two checks are not redundant.
//
// A row can be written while a deployment permits local paths and read by a
// Controller that does not, either because the toggle was removed or because
// the row predates the rule entirely. Without the sync-time check, such a row
// would be fetched by a Controller configured to refuse it.
func TestStore_ASourceAcceptedAtTheWriteIsStillCheckedAtTheSync(t *testing.T) {
	client := newStoreClient(t)
	ctx := context.Background()
	org := client.Organization.Create().SetName("network").SaveX(ctx)

	// Written through a store that permits local paths.
	permissive := project.NewEntStore(client, project.SourcePolicy{AllowLocalPath: true})
	stored, err := permissive.Create(ctx, project.Project{
		Name: "local-origin", SCMType: project.SCMGit,
		SCMURL: filepath.Join(t.TempDir(), "origin.git"), OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("Create through a permissive store = %v", err)
	}

	// Synced by a syncer that does not.
	strict := project.NewGitSyncer(t.TempDir(), nil, project.SourcePolicy{})
	got, err := strict.Sync(ctx, stored, nil)
	if err != nil {
		t.Fatalf("Sync() = %v, want a recorded failure", err)
	}
	if got.Status != project.SyncFailed {
		t.Fatalf("Sync() status = %q, want failed: the stored row is not what makes a source allowed", got.Status)
	}
	if !strings.Contains(got.Err, "PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE") {
		t.Errorf("the recorded failure does not name the toggle:\n%s", got.Err)
	}
}
