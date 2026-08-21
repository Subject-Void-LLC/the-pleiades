package schedule_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
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

	orgA, tmplA int
	orgB, tmplB int
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

	return storeFixture{
		store:  schedule.NewEntStore(client),
		client: client,
		orgA:   orgA.ID, tmplA: tmplA.ID,
		orgB: orgB.ID, tmplB: tmplB.ID,
	}
}

func (f storeFixture) newSchedule(name string) schedule.Schedule {
	return schedule.Schedule{
		Name:       name,
		TemplateID: f.tmplA,
		Enabled:    true,
		RRule:      "FREQ=DAILY",
		Timezone:   "America/New_York",
		DTStart:    time.Date(2024, 3, 8, 9, 0, 0, 0, time.UTC),
	}
}

// TestCreateDerivesTenantFromTemplate proves a schedule gets its
// organization from what it launches rather than from what the caller
// claimed, which is what makes the tenancy boundary hold even when a
// caller supplies nothing.
func TestCreateDerivesTenantFromTemplate(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	got, err := f.store.Create(ctx, f.newSchedule("nightly"))
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

// TestCreateRefusesCrossTenantTemplate covers the check ent cannot express.
func TestCreateRefusesCrossTenantTemplate(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	s := f.newSchedule("nightly")
	s.OrganizationID = f.orgB // claims tenant B
	s.TemplateID = f.tmplA    // but launches tenant A's template

	_, err := f.store.Create(ctx, s)
	if err == nil {
		t.Fatal("Create accepted a schedule whose template belongs to another tenant")
	}
	var fe schedule.FieldError
	if !errors.As(err, &fe) || fe.Field != "template" {
		t.Errorf("error = %v, want a FieldError blaming the template", err)
	}
}

func TestGetIsTenantScoped(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
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

	if _, err := f.store.Create(ctx, f.newSchedule("nightly")); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.Create(ctx, f.newSchedule("nightly"))
	if err == nil {
		t.Fatal("two schedules with the same name in one organization were accepted")
	}
	var fe schedule.FieldError
	if !errors.As(err, &fe) || fe.Field != "name" {
		t.Errorf("error = %v, want a FieldError blaming the name", err)
	}

	// The same name in a different tenant is the ordinary case.
	other := f.newSchedule("nightly")
	other.TemplateID = f.tmplB
	if _, err := f.store.Create(ctx, other); err != nil {
		t.Errorf("the same name in another organization was refused: %v", err)
	}
}

// TestClaimOccurrenceIsExactlyOnce is the duplicate-fire guard asserted
// directly at the layer that provides it. Two claimants, one winner, and
// the loser gets a typed error it can act on rather than a generic failure.
func TestClaimOccurrenceIsExactlyOnce(t *testing.T) {
	f := newStoreFixture(t)
	ctx := context.Background()

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
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

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
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
		created, err := f.store.Create(ctx, s)
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

	overdue, err := f.store.Create(ctx, f.newSchedule("overdue"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkFired(ctx, overdue.ScheduleID, time.Time{}, &past); err != nil {
		t.Fatal(err)
	}

	disabled := f.newSchedule("disabled")
	disabled.Enabled = false
	off, err := f.store.Create(ctx, disabled)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkFired(ctx, off.ScheduleID, time.Time{}, &past); err != nil {
		t.Fatal(err)
	}

	// A future one, left with whatever next_run Create computed.
	if _, err := f.store.Create(ctx, f.newSchedule("future")); err != nil {
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

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
	if err != nil {
		t.Fatal(err)
	}
	if created.NextRun == nil {
		t.Fatal("an enabled schedule should have a next run")
	}

	created.Enabled = false
	updated, err := f.store.Update(ctx, created)
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

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC)

	claim, err := f.store.ClaimOccurrence(ctx, created.ScheduleID, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.ResolveOccurrence(ctx, claim.ID, schedule.OutcomeFired, "", "job-123"); err != nil {
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

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
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

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
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

	created, err := f.store.Create(ctx, f.newSchedule("nightly"))
	if err != nil {
		t.Fatal(err)
	}

	templates := launch.NewEntStore(f.client, launch.StaticCatalog(
		launch.CatalogEntry{Kind: "runbook", Definition: "patch-edge"},
	))

	err = templates.Delete(ctx, f.tmplA)
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
	if err := templates.Delete(ctx, f.tmplA); err != nil {
		t.Errorf("deleting an unscheduled template: %v", err)
	}
}
