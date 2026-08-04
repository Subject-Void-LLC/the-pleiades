package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

func BenchmarkIterator(b *testing.B) {
	client := enttest.Open(b, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()
	const numDevices = 10000

	builders := make([]*ent.DeviceCreate, numDevices)
	for i := 0; i < numDevices; i++ {
		builders[i] = client.Device.Create().
			SetName(fmt.Sprintf("bench-router-%d", i)).
			SetType("cisco_router").
			SetProperties(map[string]interface{}{
				"host": "10.0.0.1",
			})
	}
	bulkCreateDevices(b, ctx, client, builders)

	factory := inventory.NewItemFactory()
	repo := inventory.NewEntRepository(client, factory)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		iter, err := repo.GetGroup(ctx, "all")
		if err != nil {
			b.Fatalf("failed to get group iterator: %v", err)
		}

		count := 0
		for iter.Next(ctx) {
			_ = iter.Item()
			count++
		}

		if err := iter.Error(); err != nil {
			b.Fatalf("iterator error: %v", err)
		}

		iter.Close()

		if count != numDevices {
			b.Fatalf("expected to iterate %d devices, got %d", numDevices, count)
		}
	}
}
