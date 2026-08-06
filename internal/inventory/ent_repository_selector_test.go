package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/device"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	pkginventory "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// drainNames iterates it to completion and returns the Name of every
// yielded item, failing the test on any iterator error.
func drainNames(t *testing.T, ctx context.Context, it inventory.Iterator) []string {
	t.Helper()
	defer it.Close()
	var names []string
	for it.Next(ctx) {
		names = append(names, it.Item().Name())
	}
	if err := it.Error(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}
	return names
}

// TestEntRepository_GetGroup_FiltersBySelectorGroupName is the regression
// test for the literal bug this phase fixes: entRepository.GetGroup used
// to receive a group name and never reference it again, so every
// group-targeted dispatch silently streamed the whole devices table.
// Devices are split across two real ent Groups plus some in neither;
// GetGroup with a Selector naming one group must return exactly that
// group's devices, pushed down to SQL via the Group edge, not the whole
// table.
func TestEntRepository_GetGroup_FiltersBySelectorGroupName(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()

	mkDevice := func(name string) *ent.Device {
		return client.Device.Create().
			SetName(name).
			SetType("linux_server").
			SetProperties(map[string]interface{}{"host": "10.0.0.1"}).
			SaveX(ctx)
	}

	prod1, prod2 := mkDevice("prod-1"), mkDevice("prod-2")
	staging1 := mkDevice("staging-1")
	ungrouped := mkDevice("ungrouped-1")

	client.Group.Create().SetName("prod").AddDevices(prod1, prod2).SaveX(ctx)
	client.Group.Create().SetName("staging").AddDevices(staging1).SaveX(ctx)
	_ = ungrouped

	factory := inventory.NewItemFactory()
	repo := inventory.NewEntRepository(client, factory)

	t.Run("named group returns exactly its members, each exactly once", func(t *testing.T) {
		it, err := repo.GetGroup(ctx, pkginventory.Selector{GroupName: "prod"})
		if err != nil {
			t.Fatalf("GetGroup: %v", err)
		}
		got := drainNames(t, ctx, it)
		// A count per name, not just set membership: len(got) == 2 with
		// got == ["prod-1", "prod-1"] would satisfy a membership-only
		// check while actually proving the HasGroupsWith EXISTS subquery
		// duplicated a row, exactly the failure mode this phase's own
		// doc comment (ent_repository.go) claims immunity from.
		counts := make(map[string]int, len(got))
		for _, n := range got {
			counts[n]++
		}
		want := map[string]int{"prod-1": 1, "prod-2": 1}
		if len(counts) != len(want) {
			t.Fatalf("GetGroup(prod) returned %v (counts %v), want exactly one of each of %v", got, counts, want)
		}
		for name, wantCount := range want {
			if counts[name] != wantCount {
				t.Errorf("GetGroup(prod) yielded %q %d time(s), want %d", name, counts[name], wantCount)
			}
		}
	})

	t.Run("empty selector still returns every device", func(t *testing.T) {
		it, err := repo.GetGroup(ctx, pkginventory.Selector{})
		if err != nil {
			t.Fatalf("GetGroup: %v", err)
		}
		got := drainNames(t, ctx, it)
		if len(got) != 4 {
			t.Fatalf("GetGroup({}) returned %d devices, want 4 (unfiltered)", len(got))
		}
	})

	t.Run("a nonexistent group name fails closed, not open", func(t *testing.T) {
		// Before this phase, an unrecognized or unpopulated group name
		// silently dispatched to the entire fleet (the named bug). After
		// the fix, a group with zero real members, including one that
		// does not exist at all, correctly yields zero devices rather
		// than falling back to "everything" -- fail closed, not open.
		it, err := repo.GetGroup(ctx, pkginventory.Selector{GroupName: "does-not-exist"})
		if err != nil {
			t.Fatalf("GetGroup: %v", err)
		}
		got := drainNames(t, ctx, it)
		if len(got) != 0 {
			t.Fatalf("GetGroup(does-not-exist) returned %v, want zero devices", got)
		}
	})
}

// TestEntIterator_KeysetPaginationSurvivesConcurrentWrites is the
// Adversarial Pattern Justification's evidence: a device deleted after
// its batch was already fetched must not affect later batches (unlike
// offset pagination, where OFFSET counts row position dynamically, so any
// row count change before the offset boundary shifts every batch fetched
// after it), and a device inserted ahead of the current cursor must be
// picked up exactly once, never duplicated, never skipped.
//
// This does not attempt to reproduce the *old* offset-based code failing:
// its query had no ORDER BY at all, and SQL defines no row order without
// one, so its behavior across two sequential queries is
// implementation-defined, not something a test can reliably force to
// misbehave in one fixed direction. The new keyset query is deterministic
// by construction (ORDER BY device_id, WHERE device_id > cursor), which is
// what this test actually verifies.
func TestEntIterator_KeysetPaginationSurvivesConcurrentWrites(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()

	const seeded = 1500
	const batchSize = 1000 // matches entRepository.GetGroup's hardcoded batch size

	builders := make([]*ent.DeviceCreate, seeded)
	for i := 0; i < seeded; i++ {
		builders[i] = client.Device.Create().
			SetDeviceID(fmt.Sprintf("d-%05d", i+1)). // d-00001 .. d-01500, sorts numerically
			SetName(fmt.Sprintf("host-%05d", i+1)).
			SetType("linux_server")
	}
	bulkCreateDevices(t, ctx, client, builders)

	factory := inventory.NewItemFactory()
	repo := inventory.NewEntRepository(client, factory)

	it, err := repo.GetGroup(ctx, pkginventory.Selector{})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	defer it.Close()

	seen := make(map[string]bool, seeded+1)

	// Drain exactly the first batch (1000 devices: d-00001..d-01000).
	for i := 0; i < batchSize; i++ {
		if !it.Next(ctx) {
			t.Fatalf("iterator ended early at item %d, error: %v", i, it.Error())
		}
		id := string(it.Item().ID())
		if seen[id] {
			t.Fatalf("duplicate device_id %q within the first batch", id)
		}
		seen[id] = true
	}

	// Between batches: delete an already-yielded device (must have zero
	// effect on what follows, since its device_id is behind the cursor),
	// and insert a new device ahead of the cursor (d-01000, the last id
	// of the first batch), which the next batch must pick up exactly once.
	if _, err := client.Device.Delete().Where(device.DeviceIDEQ("d-00500")).Exec(ctx); err != nil {
		t.Fatalf("deleting an already-yielded device: %v", err)
	}
	client.Device.Create().
		SetDeviceID("d-01501").
		SetName("host-01501").
		SetType("linux_server").
		SaveX(ctx)

	// Drain the rest.
	for it.Next(ctx) {
		id := string(it.Item().ID())
		if seen[id] {
			t.Fatalf("duplicate device_id %q after the concurrent mutation", id)
		}
		seen[id] = true
	}
	if err := it.Error(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}

	// Expected: every one of the 1500 seeded devices (the delete happened
	// after d-00500 was already counted above, so it does not disappear
	// from the total) plus the one new device inserted ahead of the
	// cursor. 1500 + 1 = 1501, no duplicates (the map itself proves that).
	const want = seeded + 1
	if len(seen) != want {
		t.Fatalf("total distinct devices yielded = %d, want %d", len(seen), want)
	}
	if !seen["d-00500"] {
		t.Error("the deleted device should still have been seen once, from before its deletion")
	}
	if !seen["d-01501"] {
		t.Error("the device inserted ahead of the cursor should have been picked up")
	}
}

// TestEntIterator_HonorsContextCancellation mirrors
// TestFileRepository_GetGroup_HonorsContextCancellation
// (file_repository_errors_test.go): a caller that already gave up must see
// false rather than another item. entIterator.Next threads ctx into every
// batch's own query.All(ctx) call, but the buffered fast path (returning
// an already-fetched item without a new query) did not check ctx.Err() on
// its own, so an already-cancelled context reaching the buffered branch
// used to keep yielding buffered items regardless.
func TestEntIterator_HonorsContextCancellation(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()

	client.Device.Create().SetName("web-1").SetType("linux_server").SaveX(context.Background())

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	iter, err := repo.GetGroup(context.Background(), pkginventory.Selector{})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	defer iter.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if iter.Next(ctx) {
		t.Error("expected Next to return false for an already-cancelled context")
	}
}
