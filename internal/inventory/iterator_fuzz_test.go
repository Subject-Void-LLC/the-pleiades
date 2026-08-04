package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// FuzzIteratorPagination fuzzes the iteration boundaries to ensure the offset logic
// never goes out of bounds or panics under varying DB payload sizes and arbitrary limits.
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

		iter, err := repo.GetGroup(ctx, "all")
		if err != nil {
			t.Fatalf("failed to get group iterator: %v", err)
		}
		defer iter.Close()

		count := uint(0)
		for iter.Next(ctx) {
			_ = iter.Item()
			count++
		}

		if err := iter.Error(); err != nil {
			t.Fatalf("iterator error: %v", err)
		}

		if count != dbRows {
			t.Fatalf("fuzz iteration count mismatch! expected %d got %d", dbRows, count)
		}
	})
}
