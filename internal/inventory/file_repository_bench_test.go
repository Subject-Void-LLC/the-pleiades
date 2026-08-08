package inventory_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// BenchmarkFileRepositorySave measures one full Save round trip: reading
// hosts.yaml, reading the sidecar, the version conflict check, and the two
// atomic file rewrites. This package's other benchmarks
// (factory_bench_test.go's BenchmarkFactoryHydration, and
// yaml_plugin_bench_test.go's BenchmarkParseHosts) each measure a single
// in-memory step; this one measures the full file-backed write path end to
// end, so it is not a fair apples-to-apples number against either of
// those. ent_save_bench_test.go's BenchmarkEntRepositorySave measures the
// identical GetByName/AddInfo/Save shape against the ent-backed adapter,
// so the two are directly comparable as two adapters behind the same
// Repository port.
func BenchmarkFileRepositorySave(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	hosts := []inventory.HostSpec{{ID: "bench-id", Name: "bench-host", Type: "linux_server"}}
	if err := inventory.WriteHosts(path, hosts); err != nil {
		b.Fatalf("seeding fixture: %v", err)
	}

	repo := inventory.NewFileRepository(path, inventory.NewItemFactory())
	ctx := context.Background()

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

// BenchmarkFileRepositoryGetGroup measures listing a representative
// thousand-host inventory file, the same scale BenchmarkParseHosts
// (yaml_plugin_bench_test.go) uses, so the two numbers are comparable in
// scale: this one additionally pays for reading the sidecar and hydrating
// every host into a real InventoryItem, not just parsing YAML into
// HostSpecs.
func BenchmarkFileRepositoryGetGroup(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	hosts := make([]inventory.HostSpec, 1000)
	for i := range hosts {
		hosts[i] = inventory.HostSpec{
			ID:   fmt.Sprintf("id-%d", i),
			Name: fmt.Sprintf("host-%d", i),
			Type: "linux_server",
			Properties: map[string]interface{}{
				"host": fmt.Sprintf("10.0.%d.%d", i/256, i%256),
			},
		}
	}
	if err := inventory.WriteHosts(path, hosts); err != nil {
		b.Fatalf("seeding fixture: %v", err)
	}

	repo := inventory.NewFileRepository(path, inventory.NewItemFactory())
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		iter, err := repo.GetGroup(ctx, pkginventory.Selector{})
		if err != nil {
			b.Fatalf("GetGroup: %v", err)
		}
		count := 0
		for iter.Next(ctx) {
			count++
		}
		if err := iter.Error(); err != nil {
			b.Fatalf("iterator error: %v", err)
		}
		_ = iter.Close()
		if count != len(hosts) {
			b.Fatalf("expected %d items, got %d", len(hosts), count)
		}
	}
}
