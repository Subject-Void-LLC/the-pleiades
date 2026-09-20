package schedule_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"

	// The built-in launchable types, blank-imported for exactly the reason
	// cmd/controller blank-imports them: their init() functions are the only
	// thing that registers them, and a store admitting a target of an
	// unregistered type refuses it. A suite that did not import this would
	// fail in a way that looks like a store bug and is a wiring one.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launchable/types"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"

	_ "github.com/mattn/go-sqlite3"
)

// These run against a real ent client on real SQLite rather than a mock,
// because the behaviour worth testing here is the database's: the composite
// unique index on (name, organization), the unique index on (schedule,
// occurrence_at) that IS the duplicate-fire guard, and the keyset ordering
// of the due scan. A mock store would assert only that this file's own
// assumptions are self-consistent.

type storeFixture struct {
	store  schedule.Store
	client *ent.Client

	// The launchable ids, not the template ids: a schedule points at the row
	// standing for a template rather than at the template, which is what lets
	// one schedule mechanism cover every sort of launchable thing.
	orgA, launchableA int
	orgB, launchableB int

	// The template ids too, for the one test that deletes a template and
	// expects the refusal a schedule pointing at its launchable produces.
	templateA int
}

func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()

	// A FILE-backed database with WAL, not the usual shared-cache
	// in-memory one, and the reason is the duplicate-fire test.
	//
	// SQLite's shared-cache in-memory mode answers a contending writer
	// with SQLITE_LOCKED immediately, which _busy_timeout does not cover
	// (it retries SQLITE_BUSY only). Under that mode most of the eight
	// concurrent replicas would fail before ever reaching the claim, and
	// the test would pass because only one replica got far enough to
	// launch -- not because the unique index rejected the others. That is
	// exactly the shape of a test that proves nothing.
	//
	// WAL on a real file lets writers queue and actually contend, so the
	// index is what decides the race, which is the thing under test.
	dsn := fmt.Sprintf("file:%s/schedules.db?_journal_mode=WAL&_busy_timeout=10000&_fk=1", t.TempDir())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	orgA := client.Organization.Create().SetName("network").SaveX(ctx)
	orgB := client.Organization.Create().SetName("servers").SaveX(ctx)
	invA := client.Inventory.Create().SetName("edge").SetOrganization(orgA).SaveX(ctx)
	invB := client.Inventory.Create().SetName("racks").SetOrganization(orgB).SaveX(ctx)
	tmplA := client.Template.Create().
		SetName("patch the edge").SetKind("runbook").SetDefinition("patch-edge").
		SetOrganization(orgA).SetInventory(invA).SaveX(ctx)
	tmplB := client.Template.Create().
		SetName("patch the racks").SetKind("runbook").SetDefinition("patch-racks").
		SetOrganization(orgB).SetInventory(invB).SaveX(ctx)

	// The launchable row standing for each template, written here the way the
	// real template store writes it, so these fixtures exercise the same
	// shape production produces.
	lnchA := client.Launchable.Create().
		SetType(launchable.TypeJobTemplate).SetName(tmplA.Name).
		SetOrganization(orgA).SetTemplate(tmplA).SaveX(ctx)
	lnchB := client.Launchable.Create().
		SetType(launchable.TypeJobTemplate).SetName(tmplB.Name).
		SetOrganization(orgB).SetTemplate(tmplB).SaveX(ctx)

	return storeFixture{
		// No Router on the admission: these tests exercise the store, and a
		// nil Router means "no launcher has anything to object to in
		// advance", which is honest here rather than a stubbed approval.
		store:  schedule.NewEntStore(client, launchable.Admission{Store: launchable.NewEntStore(client)}),
		client: client,
		orgA:   orgA.ID, launchableA: lnchA.ID,
		orgB: orgB.ID, launchableB: lnchB.ID,
		templateA: tmplA.ID,
	}
}

func (f storeFixture) newSchedule(name string) schedule.Schedule {
	return schedule.Schedule{
		Name:         name,
		LaunchableID: f.launchableA,
		Enabled:      true,
		RRule:        "FREQ=DAILY",
		Timezone:     "America/New_York",
		DTStart:      time.Date(2024, 3, 8, 9, 0, 0, 0, time.UTC),
	}
}

// TestCreateDerivesTenantFromTemplate proves a schedule gets its
// organization from what it launches rather than from what the caller
// claimed, which is what makes the tenancy boundary hold even when a
// caller supplies nothing.
func TestCreateDerivesTenantFromTemplate(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	got, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.OrganizationID != f.orgA {
		t.Errorf("OrganizationID = %d, want the template's own %d", got.OrganizationID, f.orgA)
	}
	if got.ScheduleID == "" {
		t.Error("Create returned no schedule id")
	}
	if got.NextRun == nil {
		t.Fatal("Create did not compute a next run")
	}
	if !got.NextRun.After(time.Now().UTC()) {
		t.Errorf("NextRun = %s, want a time in the future", got.NextRun)
	}
}

// TestCreateRefusesACrossTenantTarget covers the check ent cannot express.
func TestCreateRefusesACrossTenantTarget(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	s := f.newSchedule("nightly")
	s.OrganizationID = f.orgB      // claims tenant B
	s.LaunchableID = f.launchableA // but launches tenant A's template

	_, err := f.store.Create(ctx, s, launchable.Everything())
	if err == nil {
		t.Fatal("Create accepted a schedule whose target belongs to another tenant")
	}
	var fe schedule.FieldError
	if !errors.As(err, &fe) || fe.Field != schedule.TargetField {
		t.Errorf("error = %v, want a FieldError blaming what the schedule launches", err)
	}

	// The narrowed caller is refused the same target for the other reason, so
	// neither the submission nor the caller's own reach can cross a tenant.
	narrowed := f.newSchedule("nightly")
	if _, err := f.store.Create(ctx, narrowed, launchable.Reach{
		OrganizationID: f.orgB,
		Grants:         func(auth.Scope) bool { return true },
	}); !errors.Is(err, launchable.ErrCrossTenant) {
		t.Errorf("a caller in another organization = %v, want ErrCrossTenant", err)
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	// The other tenant must not be able to read it, and must be told it
	// does not exist rather than that it may not see it.
	if _, err := f.store.Get(ctx, f.orgB, created.ScheduleID); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("cross-tenant Get = %v, want ErrNotFound", err)
	}
	if _, err := f.store.Get(ctx, f.orgA, created.ScheduleID); err != nil {
		t.Errorf("same-tenant Get: %v", err)
	}
}

func TestCreateRefusesDuplicateNameWithinOrg(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	if _, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything()); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err == nil {
		t.Fatal("two schedules with the same name in one organization were accepted")
	}
	var fe schedule.FieldError
	if !errors.As(err, &fe) || fe.Field != "name" {
		t.Errorf("error = %v, want a FieldError blaming the name", err)
	}

	// The same name in a different tenant is the ordinary case.
	other := f.newSchedule("nightly")
	other.LaunchableID = f.launchableB
	if _, err := f.store.Create(ctx, other, launchable.Everything()); err != nil {
		t.Errorf("the same name in another organization was refused: %v", err)
	}
}

// TestClaimOccurrenceIsExactlyOnce is the duplicate-fire guard asserted
// directly at the layer that provides it. Two claimants, one winner, and
// the loser gets a typed error it can act on rather than a generic failure.
func TestClaimOccurrenceIsExactlyOnce(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	occurrenceAt := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC)

	first, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, occurrenceAt)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.Outcome != schedule.OutcomeClaimed {
		t.Errorf("a fresh claim has outcome %q, want %q", first.Outcome, schedule.OutcomeClaimed)
	}

	if _, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, occurrenceAt); !errors.Is(err, schedule.ErrAlreadyClaimed) {
		t.Fatalf("second claim = %v, want ErrAlreadyClaimed", err)
	}

	// A different occurrence of the same schedule is unaffected.
	if _, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, occurrenceAt.Add(24*time.Hour)); err != nil {
		t.Errorf("claiming a different occurrence: %v", err)
	}
}

// TestClaimIsIndifferentToLocationOfTheSameInstant proves the guard keys on
// the absolute instant, so two replicas whose process zones differ compute
// the same key. Storing a wall clock instead would let the same occurrence
// be claimed twice.
func TestClaimIsIndifferentToLocationOfTheSameInstant(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	utc := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC)
	same := utc.In(ny) // identical instant, different location

	if _, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, utc); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, same); !errors.Is(err, schedule.ErrAlreadyClaimed) {
		t.Errorf("claiming the same instant expressed in another zone = %v, want ErrAlreadyClaimed", err)
	}
}

// TestListDuePagesByKeyset proves the scan's cursor is total across
// schedules that come due at the same instant -- the case a cursor on
// next_run alone would either skip or repeat.
func TestListDuePagesByKeyset(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	now := time.Now().UTC()
	due := now.Add(-time.Hour).Truncate(time.Second)

	// Six schedules, all due at the identical instant.
	var ids []string
	for i := 0; i < 6; i++ {
		s := f.newSchedule(fmt.Sprintf("sched-%d", i))
		created, err := f.store.Create(ctx, s, launchable.Everything())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.MarkFired(ctx, created.ScheduleID, time.Time{}, &due); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, created.ScheduleID)
	}

	seen := map[string]int{}
	var cursor schedule.DueCursor
	for page := 0; page < 10; page++ {
		batch, err := f.store.ListDue(ctx, now, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(batch) == 0 {
			break
		}
		for _, s := range batch {
			seen[s.ScheduleID]++
		}
		last := batch[len(batch)-1]
		cursor = schedule.DueCursor{NextRun: *last.NextRun, ScheduleID: last.ScheduleID}
	}

	if len(seen) != len(ids) {
		t.Errorf("paged over %d schedules, want %d", len(seen), len(ids))
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("schedule %s appeared %d times across pages, want exactly 1", id, n)
		}
	}
}

// TestListDueExcludesDisabledAndFuture proves the scan selects only what is
// actually runnable, so a disabled schedule cannot be fired by a scan that
// merely forgot to check.
func TestListDueExcludesDisabledAndFuture(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	now := time.Now().UTC()
	past := now.Add(-time.Hour)

	overdue, err := f.store.Create(ctx, f.newSchedule("overdue"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkFired(ctx, overdue.ScheduleID, time.Time{}, &past); err != nil {
		t.Fatal(err)
	}

	disabled := f.newSchedule("disabled")
	disabled.Enabled = false
	off, err := f.store.Create(ctx, disabled, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkFired(ctx, off.ScheduleID, time.Time{}, &past); err != nil {
		t.Fatal(err)
	}

	// A future one, left with whatever next_run Create computed.
	if _, err := f.store.Create(ctx, f.newSchedule("future"), launchable.Everything()); err != nil {
		t.Fatal(err)
	}

	batch, err := f.store.ListDue(ctx, now, schedule.DueCursor{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 {
		t.Fatalf("ListDue returned %d schedules, want only the overdue one", len(batch))
	}
	if batch[0].ScheduleID != overdue.ScheduleID {
		t.Errorf("ListDue returned %q, want %q", batch[0].Name, "overdue")
	}
}

// TestDisablingClearsNextRun proves a disabled schedule stops advertising a
// time nothing will happen at.
func TestDisablingClearsNextRun(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if created.NextRun == nil {
		t.Fatal("an enabled schedule should have a next run")
	}

	created.Enabled = false
	updated, err := f.store.Update(ctx, created, launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if updated.NextRun != nil {
		t.Errorf("a disabled schedule still reports NextRun = %s", updated.NextRun)
	}
}

func TestResolveAndListOccurrences(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC)

	claim, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ResolveOccurrence(ctx, claim.ID, schedule.OutcomeFired, "",
		launchable.Launched{RunID: "job-123", UnifiedJobType: launchable.UnifiedJobJob}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordSkip(ctx, created.ScheduleID, at.Add(-time.Hour), schedule.ReasonMissedWindow, 0); err != nil {
		t.Fatal(err)
	}

	history, err := f.store.ListOccurrences(ctx, f.orgA, created.ScheduleID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("history has %d rows, want 2", len(history))
	}
	// Most recent first.
	if history[0].Outcome != schedule.OutcomeFired || history[0].JobID != "job-123" {
		t.Errorf("newest row = %+v, want the fired one carrying its job id", history[0])
	}
	if history[1].Outcome != schedule.OutcomeSkipped || history[1].Reason != schedule.ReasonMissedWindow {
		t.Errorf("older row = %+v, want the skipped one with a reason", history[1])
	}
}

func TestDeleteRemovesHistory(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordSkip(ctx, created.ScheduleID, time.Now().UTC(), schedule.ReasonMissedWindow, 0); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Delete(ctx, f.orgA, created.ScheduleID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Get(ctx, f.orgA, created.ScheduleID); !errors.Is(err, schedule.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	n, err := f.client.ScheduleOccurrence.Query().Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d occurrence rows survived the schedule's deletion", n)
	}
}

func TestDeleteIsTenantScoped(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Delete(ctx, f.orgB, created.ScheduleID); !errors.Is(err, schedule.ErrNotFound) {
		t.Fatalf("cross-tenant Delete = %v, want ErrNotFound", err)
	}
	if _, err := f.store.Get(ctx, f.orgA, created.ScheduleID); err != nil {
		t.Errorf("the schedule was deleted by another tenant: %v", err)
	}
}

// TestDeletingAScheduledTemplateIsRefused covers the one edge Phase 23 added
// that is deliberately NOT cascaded, and the reason it matters is what
// happens when it is missed: without the typed error, the database's own
// constraint failure reaches an HTTP handler as a 500, telling an operator
// the server is broken when in fact they asked for something reasonable
// that is being refused for a reason they can act on.
//
// It lives in this package rather than internal/launch's because the
// constraint only exists once a schedule does, and this is the package that
// creates one.
func TestDeletingAScheduledTemplateIsRefused(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"), launchable.Everything())
	if err != nil {
		t.Fatal(err)
	}

	templates := launch.NewEntStore(f.client, launch.StaticCatalog(
		launch.CatalogEntry{Kind: "runbook", Definition: "patch-edge"},
	))

	err = templates.Delete(ctx, f.templateA)
	if !errors.Is(err, launch.ErrInUse) {
		t.Fatalf("deleting a scheduled template = %v, want launch.ErrInUse", err)
	}
	// And it really did not delete it: a refusal that half-happened would
	// be worse than either outcome.
	if _, err := f.store.Get(ctx, f.orgA, created.ScheduleID); err != nil {
		t.Errorf("the schedule did not survive the refused deletion: %v", err)
	}
	if n, err := f.client.Template.Query().Count(ctx); err != nil {
		t.Fatal(err)
	} else if n != 2 {
		t.Errorf("%d templates remain, want both", n)
	}

	// Once the schedule is gone the template deletes normally, which is
	// what makes this a refusal rather than a template that can never be
	// removed.
	if err := f.store.Delete(ctx, f.orgA, created.ScheduleID); err != nil {
		t.Fatal(err)
	}
	if err := templates.Delete(ctx, f.templateA); err != nil {
		t.Errorf("deleting an unscheduled template: %v", err)
	}
}
