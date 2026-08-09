package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// BenchmarkEntRepositorySave measures one full entRepository.Save round
// trip (GetByName, one AddInfo, Save) through the real storage.UnitOfWork-
// based transaction this phase introduced: the compare-and-swap Device
// update plus the new Revision insert, both inside one committed
// transaction. It mirrors BenchmarkFileRepositorySave's own shape
// (file_repository_bench_test.go) exactly, so the two are directly
// comparable as two adapters behind the same Repository port, closing
// that file's own note that entRepository.Save previously had no
// benchmark of its own to compare against.
func BenchmarkEntRepositorySave(b *testing.B) {
	client := enttest.Open(b, "sqlite3", "file:entsavebench?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	_, err := client.Device.Create().
		SetName("bench-host").
		SetType("linux_server").
		Save(ctx)
	if err != nil {
		b.Fatalf("seeding fixture: %v", err)
	}

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item, err := repo.GetByName(ctx, "bench-host")
		if err != nil {
			b.Fatalf("GetByName: %v", err)
		}
		// A distinct key per iteration keeps AddInfo from hitting the
		// overwrite=false rejection path, so every iteration does a real
		// Save rather than a Save-then-error loop.
		if err := item.AddInfo(fmt.Sprintf("iter-%d", i), "value", true); err != nil {
			b.Fatalf("AddInfo: %v", err)
		}
		if err := repo.Save(ctx, item); err != nil {
			b.Fatalf("Save: %v", err)
		}
	}
}
