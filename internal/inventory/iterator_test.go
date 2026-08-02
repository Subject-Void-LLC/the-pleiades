package inventory_test

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent"
	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

func TestIteratorMemoryFlatline(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory flatline test in short mode")
	}

	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	// 1. Bulk insert 50,000 devices
	const numDevices = 50000
	batchSize := 5000

	for i := 0; i < numDevices; i += batchSize {
		builders := make([]*ent.DeviceCreate, batchSize)
		for j := 0; j < batchSize; j++ {
			id := i + j
			builders[j] = client.Device.Create().
				SetName(fmt.Sprintf("router-%d", id)).
				SetProperties(map[string]interface{}{
					"type": "cisco_router",
					"host": "10.0.0.1",
				})
		}
		if err := client.Device.CreateBulk(builders...).Exec(ctx); err != nil {
			t.Fatalf("failed to insert batch: %v", err)
		}
	}

	factory := inventory.NewItemFactory()
	repo := inventory.NewEntRepository(client, factory)

	// 2. Measure memory before
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// 3. Iterate
	iter, err := repo.GetGroup(ctx, "all")
	if err != nil {
		t.Fatalf("failed to get group iterator: %v", err)
	}
	defer iter.Close()

	count := 0
	for iter.Next(ctx) {
		item := iter.Item()
		if item == nil {
			t.Fatalf("iterator returned nil item")
		}
		count++
	}

	if err := iter.Error(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}

	if count != numDevices {
		t.Fatalf("expected to iterate %d devices, got %d", numDevices, count)
	}

	// 4. Measure memory after
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	// We only expect a very small memory overhead for the iterator's state and current batch buffer
	// (batch size is 1000). The total Alloc diff should be well under 5MB.
	diffMB := float64(m2.Alloc-m1.Alloc) / 1024 / 1024
	if diffMB > 5.0 {
		t.Fatalf("Memory allocation is NOT flat! Grew by %.2f MB during iteration", diffMB)
	}
	
	t.Logf("Iterator processed %d devices with memory growth of %.2f MB", count, diffMB)
}
