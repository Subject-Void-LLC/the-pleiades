package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	pkginventory "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// FuzzIteratorPagination fuzzes the iteration boundaries to ensure the
// keyset-based batching (entIterator.Next, ent_repository.go) never goes
// out of bounds, panics, skips, or duplicates a device_id under varying DB
// payload sizes, including exact batch-size multiples and their
// neighbors.
func FuzzIteratorPagination(f *testing.F) {
	f.Add(uint(0))
	f.Add(uint(1))
	f.Add(uint(999))
	f.Add(uint(1000))
	f.Add(uint(1001))
	f.Add(uint(5000))

	f.Fuzz(func(t *testing.T, dbRows uint) {
		// Cap fuzzing to 5000 rows to prevent test timeouts, but still hit all batch boundaries.
		if dbRows > 5000 {
			dbRows = 5000
		}

		client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
		defer client.Close()
		ctx := context.Background()

		deviceBuilders := make([]*ent.DeviceCreate, dbRows)
		for j := uint(0); j < dbRows; j++ {
			deviceBuilders[j] = client.Device.Create().
				SetName(fmt.Sprintf("fuzz-%d", j)).
				SetType("linux_server")
		}

		if dbRows > 0 {
			bulkCreateDevices(t, ctx, client, deviceBuilders)
		}

		factory := inventory.NewItemFactory()
		repo := inventory.NewEntRepository(client, factory)

		iter, err := repo.GetGroup(ctx, pkginventory.Selector{})
		if err != nil {
			t.Fatalf("failed to get group iterator: %v", err)
		}
		defer iter.Close()

		// Collecting every yielded device_id, not just a count, is what
		// actually proves "never skips or duplicates a device_id": a
		// count-only check cannot distinguish "every row seen once" from
		// "some row seen twice and a different row never seen," which a
		// count comparison alone would miss whenever those two errors
		// happen to cancel out.
		seen := make(map[string]bool, dbRows)
		for iter.Next(ctx) {
			id := string(iter.Item().ID())
			if seen[id] {
				t.Fatalf("device_id %q yielded more than once (dbRows=%d)", id, dbRows)
			}
			seen[id] = true
		}

		if err := iter.Error(); err != nil {
			t.Fatalf("iterator error: %v", err)
		}

		if uint(len(seen)) != dbRows {
			t.Fatalf("fuzz iteration count mismatch! expected %d distinct device_ids, got %d", dbRows, len(seen))
		}
	})
}
