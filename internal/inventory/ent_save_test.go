package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginv "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// newTestRepo spins up a real in-memory SQLite database and returns a
// repository over it. Per RULE 0 these tests exercise the actual ent write
// path, not a mock: the whole point of this code is that a value survives a
// round trip through storage, which a mocked repository cannot demonstrate.
//
// Each test gets its own database name so shared-cache SQLite does not leak
// rows between tests.
func newTestRepo(t *testing.T) (inventory.Repository, *ent.Client) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	return inventory.NewEntRepository(client, inventory.NewItemFactory()), client
}

// seedDevice inserts one linux_server row and returns its name.
func seedDevice(t *testing.T, client *ent.Client, name string) {
	t.Helper()

	_, err := client.Device.Create().
		SetName(name).
		SetType("linux_server").
		SetProperties(map[string]interface{}{
			"host": "10.0.0.9",
		}).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seeding device %s: %v", name, err)
	}
}

// TestSave_RoundTripsPropertiesVersionAndHistory is the core regression
// test for this change. Before it, Repository was read-only: a Revision
// recorded by AddInfo lived in memory and was discarded, and every hydrated
// item restarted at version 0.
func TestSave_RoundTripsPropertiesVersionAndHistory(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)
	seedDevice(t, client, "web-1")

	loaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got := loaded.Version(); got != 0 {
		t.Fatalf("fresh device version = %d, want 0", got)
	}

	if err := loaded.AddInfo("kernel", "6.6.1", false); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}
	if err := loaded.AddInfo("kernel", "6.6.2", true); err != nil {
		t.Fatalf("AddInfo overwrite: %v", err)
	}
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName after save: %v", err)
	}

	// The property must have survived.
	if got, _ := reloaded.Properties().String("kernel"); got != "6.6.2" {
		t.Errorf("kernel after reload = %q, want %q", got, "6.6.2")
	}

	// The version must have survived. This is the assertion that fails
	// against the old code, where NewBase never restored it.
	if got := reloaded.Version(); got != 2 {
		t.Errorf("version after reload = %d, want 2", got)
	}

	// The audit trail must have survived, in order, with old and new values.
	history := reloaded.History()
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2: %+v", len(history), history)
	}
	if history[0].Version != 1 || history[1].Version != 2 {
		t.Errorf("history versions = %d,%d, want 1,2", history[0].Version, history[1].Version)
	}
	if history[0].OldValue != nil {
		t.Errorf("first revision OldValue = %v, want nil (property did not exist before)", history[0].OldValue)
	}
	if history[0].NewValue != "6.6.1" {
		t.Errorf("first revision NewValue = %v, want 6.6.1", history[0].NewValue)
	}
	if history[1].OldValue != "6.6.1" {
		t.Errorf("second revision OldValue = %v, want 6.6.1", history[1].OldValue)
	}
	if history[1].Field != "kernel" {
		t.Errorf("second revision Field = %q, want kernel", history[1].Field)
	}
}

// TestSave_PersistsTagsAndSource is the regression test for the chain
// audit's provenance finding (IMPLEMENTATION.md Phase W4): Save used to
// set only Properties, Version, and State, never Tags, Source, or
// SourceSyncedAt, so those columns could never be written back through
// the domain Repository port at all. It queries the row directly through
// a second, independent ent client call after Save, rather than through
// GetByName, so this cannot pass merely because toRecord reads the
// column back correctly; the SQL Save itself issues must have written it.
func TestSave_PersistsTagsAndSource(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)

	syncedAt := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	_, err := client.Device.Create().
		SetName("web-1").
		SetType("linux_server").
		SetTags([]string{"prod", "web"}).
		SetSource("netbox").
		SetSourceSyncedAt(syncedAt).
		Save(ctx)
	if err != nil {
		t.Fatalf("seeding device: %v", err)
	}

	loaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := loaded.AddInfo("checked", "true", true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}

	row, err := client.Device.Query().Where(device.NameEQ("web-1")).Only(ctx)
	if err != nil {
		t.Fatalf("querying row directly: %v", err)
	}
	if got := row.Tags; len(got) != 2 || got[0] != "prod" || got[1] != "web" {
		t.Errorf("stored tags after Save = %v, want [prod web]", got)
	}
	if row.Source != "netbox" {
		t.Errorf("stored source after Save = %q, want netbox", row.Source)
	}
	if row.SourceSyncedAt == nil || !row.SourceSyncedAt.Equal(syncedAt) {
		t.Errorf("stored source_synced_at after Save = %v, want %v", row.SourceSyncedAt, syncedAt)
	}
}

// TestSave_RejectsConcurrentWriteAndPreservesFirstWriter proves the write
// is a compare-and-swap rather than last-write-wins. The second writer must
// fail AND must not have overwritten the first writer's value, which is the
// part that actually matters: an error that still corrupted the row would
// be worse than no error at all.
func TestSave_RejectsConcurrentWriteAndPreservesFirstWriter(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)
	seedDevice(t, client, "web-1")

	first, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("second load: %v", err)
	}

	if err := first.AddInfo("owner", "team-a", true); err != nil {
		t.Fatalf("first AddInfo: %v", err)
	}
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("first Save should succeed: %v", err)
	}

	// The second writer read the row before the first wrote it, so its
	// change was computed against state that no longer exists.
	if err := second.AddInfo("owner", "team-b", true); err != nil {
		t.Fatalf("second AddInfo: %v", err)
	}
	err = repo.Save(ctx, second)
	if !errors.Is(err, inventory.ErrVersionConflict) {
		t.Fatalf("second Save error = %v, want ErrVersionConflict", err)
	}

	// The losing write must not have landed.
	reloaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, _ := reloaded.Properties().String("owner"); got != "team-a" {
		t.Errorf("owner = %q, want team-a: the rejected write corrupted the row", got)
	}
	if got := reloaded.Version(); got != 1 {
		t.Errorf("version = %d, want 1: the rejected write still bumped the version", got)
	}
}

// TestSave_NoOpWhenNothingChanged guards against burning a version on a
// save that has nothing to record, which would produce a version bump no
// revision explains.
func TestSave_NoOpWhenNothingChanged(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)
	seedDevice(t, client, "web-1")

	loaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("Save with no changes should be a no-op, got: %v", err)
	}

	reloaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Version(); got != 0 {
		t.Errorf("version = %d, want 0: an unchanged save bumped the version", got)
	}
	if got := len(reloaded.History()); got != 0 {
		t.Errorf("history length = %d, want 0", got)
	}
}

// TestSave_RemovalRecordsNilNewValue covers the RemoveInfo path, where the
// revision's NewValue is deliberately absent rather than a JSON null.
func TestSave_RemovalRecordsNilNewValue(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)
	seedDevice(t, client, "web-1")

	loaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := loaded.RemoveInfo("host"); err != nil {
		t.Fatalf("RemoveInfo: %v", err)
	}
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reloaded.Properties().String("host"); ok {
		t.Error("host property still present after RemoveInfo and reload")
	}

	history := reloaded.History()
	if len(history) != 1 {
		t.Fatalf("history length = %d, want 1", len(history))
	}
	if history[0].NewValue != nil {
		t.Errorf("removal NewValue = %v, want nil", history[0].NewValue)
	}
	if history[0].OldValue != "10.0.0.9" {
		t.Errorf("removal OldValue = %v, want 10.0.0.9", history[0].OldValue)
	}
}

// TestLifecycleState_RoundTrip walks every state through storage. The
// lifecycle column is new; before it, every hydrated device was hardcoded
// active regardless of what it actually was.
func TestLifecycleState_RoundTrip(t *testing.T) {
	states := []pkginv.LifecycleState{
		pkginv.StateDiscovered,
		pkginv.StateQuarantined,
		pkginv.StateOnboarding,
		pkginv.StateActive,
		pkginv.StateSimulateLocked,
		pkginv.StateUnreachable,
		pkginv.StateDecommissioning,
		pkginv.StateArchived,
	}

	for _, want := range states {
		t.Run(want.String(), func(t *testing.T) {
			ctx := context.Background()
			repo, client := newTestRepo(t)

			_, err := client.Device.Create().
				SetName("dev-1").
				SetType("linux_server").
				SetState(want.String()).
				Save(ctx)
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			loaded, err := repo.GetByName(ctx, "dev-1")
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if got := loaded.State(); got != want {
				t.Errorf("state = %v, want %v", got, want)
			}
			if got := loaded.State().CanExecute(); got != (want == pkginv.StateActive) {
				t.Errorf("CanExecute() = %v for state %v", got, want)
			}
		})
	}
}

// TestGetByName_RejectsUnknownStoredState proves an unrecognized lifecycle
// value fails loudly instead of being coerced to active. Active is the only
// state that permits execution, so a silent default here would let an
// archived or quarantined device accept work.
func TestGetByName_RejectsUnknownStoredState(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)

	_, err := client.Device.Create().
		SetName("dev-1").
		SetType("linux_server").
		SetState("a-state-from-a-newer-build").
		Save(ctx)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.GetByName(ctx, "dev-1"); err == nil {
		t.Fatal("GetByName accepted an unrecognized lifecycle state instead of failing")
	}
}

// TestGetByName_OrdersHistoryByVersionNotInsertion proves the audit trail
// is returned oldest-first even when the rows come back in another order.
// Ordering must follow the monotonic per-device version rather than
// changed_at, because wall-clock time can move backward.
func TestGetByName_OrdersHistoryByVersionNotInsertion(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)
	seedDevice(t, client, "web-1")

	dev, err := client.Device.Query().Only(ctx)
	if err != nil {
		t.Fatalf("query device: %v", err)
	}

	// Insert deliberately out of order, and with changed_at running
	// backwards relative to version, so a sort on time would disagree.
	base := time.Now().UTC()
	for _, v := range []uint64{3, 1, 2} {
		_, err := client.Revision.Create().
			SetVersion(v).
			SetChangedAt(base.Add(-time.Duration(v) * time.Hour)).
			SetFieldName(fmt.Sprintf("field-%d", v)).
			SetDeviceID(dev.ID).
			Save(ctx)
		if err != nil {
			t.Fatalf("create revision %d: %v", v, err)
		}
	}

	loaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}

	history := loaded.History()
	if len(history) != 3 {
		t.Fatalf("history length = %d, want 3", len(history))
	}
	for i, want := range []uint64{1, 2, 3} {
		if history[i].Version != want {
			t.Errorf("history[%d].Version = %d, want %d (trail not ordered by version)", i, history[i].Version, want)
		}
	}
}

// TestSave_RollsBackDeviceUpdateWhenALaterRevisionInsertFails is the
// Adversarial Pattern Justification's load-bearing proof for
// storage.UnitOfWork: a genuine, unsimulated mid-transaction failure
// (RULE 0, no mock) must roll back everything Save already wrote in the
// same call, not just the one statement that failed.
//
// The setup produces three pending revisions where the first's insert
// succeeds for real before the second's genuinely fails: AddInfo("k1",
// ...) records a marshalable revision and leaves k1 in the live property
// bag; AddInfo("k2", <a channel>) records a second revision whose
// NewValue encoding/json cannot marshal, which is exactly what makes its
// own Revision.Create().Save fail when Save reaches it; RemoveInfo("k2")
// then takes the channel back out of the live property bag before Save
// runs, so the Device row's own SetProperties call (which only sees the
// current bag, not the history) succeeds and is not what triggers the
// failure being tested. This isolates the claim under test to "a later
// revision insert failing rolls back an earlier revision insert that
// already succeeded, in the same transaction" rather than conflating it
// with "an unmarshalable live property fails the whole Save," which is a
// different (real, but less interesting) failure mode entirely.
func TestSave_RollsBackDeviceUpdateWhenALaterRevisionInsertFails(t *testing.T) {
	ctx := context.Background()
	repo, client := newTestRepo(t)
	seedDevice(t, client, "atomic-host")

	loaded, err := repo.GetByName(ctx, "atomic-host")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	beforeProperties := loaded.Properties().Raw()
	beforeVersion := loaded.Version()

	if err := loaded.AddInfo("k1", "marshalable", false); err != nil {
		t.Fatalf("AddInfo k1: %v", err)
	}
	// A channel is a value encoding/json genuinely cannot marshal ("json:
	// unsupported type: chan int"), not a value this test pretends is bad.
	unmarshalable := make(chan int)
	if err := loaded.AddInfo("k2", unmarshalable, false); err != nil {
		t.Fatalf("AddInfo k2: %v", err)
	}
	// Removing k2 takes it out of the live property bag Save's Device
	// update marshals, so only the *revision* recording k2's addition
	// (and this removal) still carries the unmarshalable value.
	if err := loaded.RemoveInfo("k2"); err != nil {
		t.Fatalf("RemoveInfo k2: %v", err)
	}

	if err := repo.Save(ctx, loaded); err == nil {
		t.Fatalf("expected Save to fail on the unmarshalable revision, got nil error")
	}

	reloaded, err := repo.GetByName(ctx, "atomic-host")
	if err != nil {
		t.Fatalf("GetByName after failed Save: %v", err)
	}
	if reloaded.Version() != beforeVersion {
		t.Errorf("device version = %d, want unchanged %d: the failed Save's Device update was not rolled back", reloaded.Version(), beforeVersion)
	}
	if got := reloaded.Properties().Raw(); fmt.Sprint(got) != fmt.Sprint(beforeProperties) {
		t.Errorf("device properties = %v, want unchanged %v: the failed Save's Device update was not rolled back", got, beforeProperties)
	}
	if len(reloaded.History()) != 0 {
		t.Errorf("history length = %d, want 0: the revision that succeeded before the failing one was not rolled back", len(reloaded.History()))
	}
}
