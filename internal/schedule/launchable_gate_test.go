// This file is Phase 21's C1 gate, stated as the roadmap states it: "A
// schedule attached to a project sync and a schedule attached to a job
// template run through one code path, with an archtest proving no consumer
// branches on which."
//
// The archtest half lives in internal/archtest. This is the behavioural half,
// and it is written to be the thing it claims rather than a mock of it:
//
//   - a real SQLite database, opened through the same entry point the
//     Controller opens one with, so the schema is the migrations' and not
//     Schema.Create's;
//   - the real template store and the real project store, so the launchable
//     rows are written by the code that writes them in production;
//   - a real git repository on disk and the real GitSyncer, so the project
//     sync genuinely clones something;
//   - the real Dispatcher over a real in-process bus and a real runbook
//     source, so the job is created, recorded and published;
//   - the real launchable.Router and the real Scanner.
//
// Two schedules come due. ONE Sweep runs. What has to be true afterwards is
// that both fired, through the same call, and that each produced the sort of
// run its own type produces: a job for the template, a sync attempt for the
// project, each recorded on its occurrence so somebody can find it later.
//
// It lives in this package rather than in cmd/controller because it is about
// the seam rather than about the binary: the release gate over three real
// controller processes is a separate test and stays where it is.
package schedule_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"

	// The built-in launch KINDS, blank-imported as both composition roots do:
	// a template is validated against its kind's descriptor at the write, so a
	// suite without this would refuse every template as an unknown kind.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types/jobtemplate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types/projectsync"
	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

// gate is one assembled deployment's worth of the launch path.
type gate struct {
	client    *ent.Client
	templates launch.Store
	projects  project.Store
	runner    *project.Runner
	jobs      dispatch.JobStore
	store     schedule.Store
	router    *launchable.Router

	orgID    int
	originAt string // the seeded repository's own HEAD
}

// newGate assembles the real stack over a real database.
func newGate(t *testing.T) *gate {
	t.Helper()
	ctx := context.Background()

	// A real file database through the production opener, which applies the
	// versioned migrations. enttest would build the schema with Schema.Create
	// instead, and this gate's whole subject is a schema change.
	client, err := ent.OpenDatabase(ctx, ent.Config{
		DSN: filepath.Join(t.TempDir(), "gate.sqlite"),
	})
	if err != nil {
		t.Fatalf("opening the gate database: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	org := client.Organization.Create().SetName("gate").SaveX(ctx)
	inv := client.Inventory.Create().SetName("gate-inventory").SetOrganization(org).SaveX(ctx)

	// The runbook the template runs, on disk, read by the real source.
	runbookDir := t.TempDir()
	const runbookID = "gate-runbook"
	body := "id: " + runbookID + "\nhosts: []\ntasks: []\n"
	if err := os.WriteFile(filepath.Join(runbookDir, runbookID+".yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing the gate runbook: %v", err)
	}
	runbooks, err := runbook.NewDirSource(runbookDir)
	if err != nil {
		t.Fatalf("opening the runbook source: %v", err)
	}

	templates := launch.NewEntStore(client, launch.StaticCatalog(
		launch.CatalogEntry{Kind: "runbook", Definition: runbookID},
	))
	tmpl, err := templates.Create(ctx, launch.Template{
		Name:        "gate-template",
		KindName:    "runbook",
		Definition:  runbookID,
		InventoryID: inv.ID,
	})
	if err != nil {
		t.Fatalf("creating the gate template: %v", err)
	}
	if tmpl.LaunchableID == 0 {
		t.Fatal("the template store wrote no launchable row, so nothing could schedule it")
	}

	// A real repository, so the sync clones rather than pretends to.
	origin, head := newGateRepo(t)
	// Local paths permitted, because this gate clones a real repository it
	// created in a temporary directory: that is the whole point of it being a
	// gate rather than a mock, and a deployment refuses such a source by
	// default (internal/project/source.go).
	localSource := project.SourcePolicy{AllowLocalPath: true}
	projects := project.NewEntStore(client, localSource)
	proj, err := projects.Create(ctx, project.Project{
		Name:           "gate-project",
		SCMType:        project.SCMGit,
		SCMURL:         origin,
		OrganizationID: org.ID,
	})
	if err != nil {
		t.Fatalf("creating the gate project: %v", err)
	}
	if proj.LaunchableID == 0 {
		t.Fatal("the project store wrote no launchable row, so nothing could schedule it")
	}

	jobs := dispatch.NewEntJobStore(client)
	dispatcher := api.NewDispatcher(runbooks, jobs, event.NewInProcessBus(),
		api.WithTemplates(templates))
	runner := project.NewRunner(projects, project.NewGitSyncer(t.TempDir(), nil, localSource), nil)
	t.Cleanup(func() { _ = runner.Shutdown(context.Background()) })

	router, err := launchable.NewRouter(map[string]launchable.Launcher{
		jobtemplate.Type: dispatcher,
		projectsync.Type: runner,
	})
	if err != nil {
		t.Fatalf("building the launchable router: %v", err)
	}

	return &gate{
		client:    client,
		templates: templates,
		projects:  projects,
		runner:    runner,
		jobs:      jobs,
		store: schedule.NewEntStore(client, launchable.Admission{
			Store:  launchable.NewEntStore(client),
			Router: router,
		}),
		router:   router,
		orgID:    org.ID,
		originAt: head,
	}
}

// newGateRepo seeds a real git repository with one commit and returns its path
// and HEAD, so the gate can assert the sync landed on the right revision
// rather than merely that it reported success.
func newGateRepo(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("PlainInit: %v", err)
	}
	tree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site.yml"), []byte("- hosts: all\n"), 0o600); err != nil {
		t.Fatalf("writing the repository's playbook: %v", err)
	}
	if _, err := tree.Add("site.yml"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	hash, err := tree.Commit("seed", &gogit.CommitOptions{
		Author: &object.Signature{Name: "gate", Email: "gate@example.test", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return dir, hash.String()
}

// overdue creates a schedule already due, by writing next_run into the past
// through the store's own MarkFired rather than by waiting out a recurrence.
func (g *gate) overdue(t *testing.T, name string, launchableID int, at time.Time) schedule.Schedule {
	t.Helper()
	ctx := context.Background()

	created, err := g.store.Create(ctx, schedule.Schedule{
		Name:         name,
		LaunchableID: launchableID,
		Enabled:      true,
		RRule:        "FREQ=HOURLY",
		Timezone:     "UTC",
		DTStart:      at.Add(-2 * time.Hour),
	}, launchable.Everything())
	if err != nil {
		t.Fatalf("creating the %s schedule: %v", name, err)
	}
	if err := g.store.MarkFired(ctx, created.ScheduleID, at.Add(-time.Hour), &at); err != nil {
		t.Fatalf("making the %s schedule due: %v", name, err)
	}
	return created
}

// occurrenceOf reads a schedule's single occurrence.
func (g *gate) occurrenceOf(t *testing.T, scheduleID string) schedule.Occurrence {
	t.Helper()
	occs, err := g.store.ListOccurrences(context.Background(), g.orgID, scheduleID, 10)
	if err != nil {
		t.Fatalf("listing occurrences of %s: %v", scheduleID, err)
	}
	if len(occs) != 1 {
		t.Fatalf("%s has %d occurrences, want exactly 1: %+v", scheduleID, len(occs), occs)
	}
	return occs[0]
}

// TestC1Gate_OneSweepFiresATemplateAndAProjectSync is the gate.
func TestC1Gate_OneSweepFiresATemplateAndAProjectSync(t *testing.T) {
	g := newGate(t)
	ctx := context.Background()

	tmpl, err := g.templates.List(ctx, launch.Query{Limit: 1})
	if err != nil || len(tmpl) != 1 {
		t.Fatalf("reading the seeded template back: %v (%d rows)", err, len(tmpl))
	}
	projects, err := g.projects.List(ctx, project.Query{Limit: 1})
	if err != nil || len(projects) != 1 {
		t.Fatalf("reading the seeded project back: %v (%d rows)", err, len(projects))
	}

	due := time.Now().UTC().Truncate(time.Hour)
	jobSchedule := g.overdue(t, "nightly-job", tmpl[0].LaunchableID, due)
	syncSchedule := g.overdue(t, "nightly-sync", projects[0].LaunchableID, due)

	// ONE sweep, through the one scanner, with no knowledge of what either
	// schedule points at.
	scanner := schedule.NewScanner(g.store, g.router,
		schedule.WithClock(func() time.Time { return due.Add(time.Minute) }))
	scanner.Sweep(ctx)

	// The template's schedule produced a job, recorded as a job.
	jobOcc := g.occurrenceOf(t, jobSchedule.ScheduleID)
	if jobOcc.Outcome != schedule.OutcomeFired {
		t.Fatalf("the template's occurrence = %q (%s), want fired", jobOcc.Outcome, jobOcc.Reason)
	}
	if jobOcc.UnifiedJobType != launchable.UnifiedJobJob {
		t.Errorf("the template's occurrence names run type %q, want %q",
			jobOcc.UnifiedJobType, launchable.UnifiedJobJob)
	}
	job, _, err := g.jobs.Get(ctx, jobOcc.JobID)
	if err != nil {
		t.Fatalf("the occurrence names run %q, which is not a job: %v", jobOcc.JobID, err)
	}
	if want := schedule.ScheduleActor(jobSchedule.ScheduleID); job.Actor != want {
		t.Errorf("job actor = %q, want %q so an unexpected job leads back to its schedule", job.Actor, want)
	}

	// The project's schedule produced a sync attempt, recorded as a project
	// update. The clone runs in the background, exactly as a person pressing
	// Sync leaves it, so the gate waits for the runner rather than polling.
	syncOcc := g.occurrenceOf(t, syncSchedule.ScheduleID)
	if syncOcc.Outcome != schedule.OutcomeFired {
		t.Fatalf("the project's occurrence = %q (%s), want fired", syncOcc.Outcome, syncOcc.Reason)
	}
	if syncOcc.UnifiedJobType != launchable.UnifiedJobProjectUpdate {
		t.Errorf("the project's occurrence names run type %q, want %q",
			syncOcc.UnifiedJobType, launchable.UnifiedJobProjectUpdate)
	}

	g.runner.Wait()
	runs, err := g.projects.ListSyncRuns(ctx, projects[0].ID, 10)
	if err != nil {
		t.Fatalf("listing the project's sync attempts: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("the project has %d sync attempts, want exactly 1: %+v", len(runs), runs)
	}
	if got := fmt.Sprintf("%d", runs[0].ID); got != syncOcc.JobID {
		t.Errorf("the occurrence names run %q, but the attempt recorded is %q", syncOcc.JobID, got)
	}
	if want := schedule.ScheduleActor(syncSchedule.ScheduleID); runs[0].Actor != want {
		t.Errorf("sync attempt actor = %q, want %q so an unexplained clone leads back to its schedule",
			runs[0].Actor, want)
	}
	if runs[0].Status != project.SyncSucceeded {
		t.Errorf("sync attempt = %q (%s), want it to have succeeded", runs[0].Status, runs[0].Err)
	}
	if runs[0].Revision != g.originAt {
		t.Errorf("sync landed on %q, want the repository's own HEAD %q", runs[0].Revision, g.originAt)
	}

	// And a job was created for the template's schedule only: the sync did not
	// produce one, which is what "the same path, different sorts of run" means.
	listed, err := g.jobs.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("listing jobs: %v", err)
	}
	if len(listed) != 1 {
		t.Errorf("%d jobs were created, want exactly the template's one", len(listed))
	}
}

// TestC1Gate_ATargetAlreadyRunningIsSkippedNotFailed covers the collision a
// recurring sync will eventually hit, and the distinction that keeps it from
// turning into a run of failures: a project mid-clone is a skip with a reason,
// and the schedule advances.
func TestC1Gate_ATargetAlreadyRunningIsSkippedNotFailed(t *testing.T) {
	g := newGate(t)
	ctx := context.Background()

	projects, err := g.projects.List(ctx, project.Query{Limit: 1})
	if err != nil || len(projects) != 1 {
		t.Fatalf("reading the seeded project back: %v (%d rows)", err, len(projects))
	}

	due := time.Now().UTC().Truncate(time.Hour)
	sched := g.overdue(t, "nightly-sync", projects[0].LaunchableID, due)

	// Claim the project first, standing in for a clone still running when the
	// schedule comes due.
	if _, err := g.projects.BeginSync(ctx, projects[0].ID, "somebody"); err != nil {
		t.Fatalf("claiming the project: %v", err)
	}

	scanner := schedule.NewScanner(g.store, g.router,
		schedule.WithClock(func() time.Time { return due.Add(time.Minute) }))
	scanner.Sweep(ctx)

	occ := g.occurrenceOf(t, sched.ScheduleID)
	if occ.Outcome != schedule.OutcomeSkipped {
		t.Errorf("occurrence = %q, want it skipped rather than failed", occ.Outcome)
	}
	if occ.Reason != schedule.ReasonAlreadyRunning {
		t.Errorf("reason = %q, want %q", occ.Reason, schedule.ReasonAlreadyRunning)
	}
	if occ.JobID != "" {
		t.Errorf("the skipped occurrence names run %q, want none", occ.JobID)
	}

	// The schedule moved on rather than staying stuck on the same occurrence:
	// a second sweep adds nothing, and next_run is in the future.
	scanner.Sweep(ctx)
	if got := g.occurrenceOf(t, sched.ScheduleID); got.ID != occ.ID {
		t.Errorf("a second sweep wrote another occurrence (%d then %d), so the collision loops", occ.ID, got.ID)
	}
	after, err := g.store.Get(ctx, g.orgID, sched.ScheduleID)
	if err != nil {
		t.Fatalf("reading the schedule back: %v", err)
	}
	if after.NextRun == nil || !after.NextRun.After(due) {
		t.Errorf("next run = %v, want it advanced past the occurrence that collided", after.NextRun)
	}
}

// TestC1Gate_AnUnknownTypeNeedsNoChangeHere is the open-registry claim, tested
// rather than asserted: a launchable type this repository has never heard of
// fires through the same store and the same scanner, with no edit to either.
//
// It is the behavioural counterpart to the archtest's source scan. A consumer
// that branched on type by some mechanism a scan cannot see would fail here.
func TestC1Gate_AnUnknownTypeNeedsNoChangeHere(t *testing.T) {
	// The gate is assembled BEFORE the new type is registered, because
	// NewRouter refuses to build when a registered type has no launcher: the
	// strictness this relies on later is the same strictness that would
	// otherwise refuse the gate's own two-launcher router.
	g := newGate(t)
	ctx := context.Background()

	restore := launchable.SnapshotForTest()
	t.Cleanup(restore)

	const terraform = "terraform_workspace"
	if err := launchable.Register(launchable.Descriptor{
		Type:           terraform,
		Label:          "Terraform workspace",
		UnifiedJobType: "terraform_apply",
		LaunchScope:    "runbook:execute",
	}); err != nil {
		t.Fatalf("registering a type this build has never heard of: %v", err)
	}

	// A launchable row of that type, written straight to the database because
	// no store owns it: what is under test is that the schedule path does not
	// care which store would have.
	//
	// The row still needs a target, because the schema's CHECK requires
	// exactly one pointer to be set, and its pointer columns are per type.
	// That is the one thing a genuinely new launchable type WOULD need here
	// that this test cannot skip: a column and a migration. What it would not
	// need is any change to this package, the store or the scanner, which is
	// what this test is for, so the row borrows a template of its own to
	// satisfy the constraint while carrying the new type's key.
	org := g.client.Organization.Query().FirstX(ctx)
	inv := g.client.Inventory.Query().FirstX(ctx)
	borrowed := g.client.Template.Create().
		SetName("terraform-stand-in").
		SetKind("runbook").
		SetDefinition("gate-runbook").
		SetOrganization(org).
		SetInventory(inv).
		SaveX(ctx)
	row := g.client.Launchable.Create().
		SetType(terraform).
		SetName("edge-network").
		SetOrganization(org).
		SetTemplate(borrowed).
		SaveX(ctx)

	launched := &recordingLauncher{}
	router, err := launchable.NewRouter(map[string]launchable.Launcher{
		jobtemplate.Type: launched,
		projectsync.Type: launched,
		terraform:        launched,
	})
	if err != nil {
		t.Fatalf("building a router over the new type: %v", err)
	}

	store := schedule.NewEntStore(g.client, launchable.Admission{
		Store:  launchable.NewEntStore(g.client),
		Router: router,
	})
	due := time.Now().UTC().Truncate(time.Hour)
	created, err := store.Create(ctx, schedule.Schedule{
		Name:         "apply-nightly",
		LaunchableID: row.ID,
		Enabled:      true,
		RRule:        "FREQ=HOURLY",
		Timezone:     "UTC",
		DTStart:      due.Add(-2 * time.Hour),
	}, launchable.Everything())
	if err != nil {
		t.Fatalf("scheduling a type this build has never heard of: %v", err)
	}
	if err := store.MarkFired(ctx, created.ScheduleID, due.Add(-time.Hour), &due); err != nil {
		t.Fatalf("making it due: %v", err)
	}

	scanner := schedule.NewScanner(store, router,
		schedule.WithClock(func() time.Time { return due.Add(time.Minute) }))
	scanner.Sweep(ctx)

	calls := launched.calls()
	if len(calls) != 1 {
		t.Fatalf("launched %d times, want exactly 1", len(calls))
	}
	if calls[0].targetType != terraform {
		t.Errorf("launched target type %q, want %q", calls[0].targetType, terraform)
	}

	occs, err := store.ListOccurrences(ctx, org.ID, created.ScheduleID, 10)
	if err != nil {
		t.Fatalf("listing occurrences: %v", err)
	}
	if len(occs) != 1 || occs[0].Outcome != schedule.OutcomeFired {
		t.Fatalf("occurrences = %+v, want one fired", occs)
	}
	// The run type recorded is the new type's own, stamped from its descriptor
	// rather than from anything this package knows.
	if occs[0].UnifiedJobType != "terraform_apply" {
		t.Errorf("recorded run type %q, want the descriptor's own", occs[0].UnifiedJobType)
	}
}
