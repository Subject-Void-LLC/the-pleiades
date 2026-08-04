package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	pkginv "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// TestFileRepository_ConcurrentSaveDifferentHosts races N goroutines that
// each load, mutate, and save a DIFFERENT host through the same
// repository. None should conflict with another: Save's mutex only
// serializes disk access, it does not fail a save just because another
// save is in flight for an unrelated host.
func TestFileRepository_ConcurrentSaveDifferentHosts(t *testing.T) {
	ctx := context.Background()
	const n = 20

	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	hosts := make([]inventory.HostSpec, n)
	for i := 0; i < n; i++ {
		hosts[i] = inventory.HostSpec{
			ID:   fmt.Sprintf("id-%d", i),
			Name: fmt.Sprintf("host-%d", i),
			Type: "linux_server",
		}
	}
	seedHosts(t, path, hosts)

	repo := inventory.NewFileRepository(path, inventory.NewItemFactory())

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			item, err := repo.GetByName(ctx, fmt.Sprintf("host-%d", i))
			if err != nil {
				errs[i] = fmt.Errorf("GetByName: %w", err)
				return
			}
			if err := item.AddInfo("touched", "yes", true); err != nil {
				errs[i] = fmt.Errorf("AddInfo: %w", err)
				return
			}
			errs[i] = repo.Save(ctx, item)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("host-%d: %v", i, err)
		}
	}

	// Every host must have landed its own change with no false conflict
	// and no cross-talk between unrelated rows.
	for i := 0; i < n; i++ {
		reloaded, err := repo.GetByName(ctx, fmt.Sprintf("host-%d", i))
		if err != nil {
			t.Fatalf("reload host-%d: %v", i, err)
		}
		if got := reloaded.Version(); got != 1 {
			t.Errorf("host-%d version = %d, want 1", i, got)
		}
		if got, ok := reloaded.Properties().String("touched"); !ok || got != "yes" {
			t.Errorf("host-%d touched = %q, %v; want yes, true", i, got, ok)
		}
	}
}

// TestFileRepository_ConcurrentSaveSameHost mirrors internal/lock's
// thundering-herd test style (nats_test.go's TestThunderingHerdLocking)
// applied to Save: N goroutines each hold their own loaded instance of the
// SAME host, all mutate it, and race to Save through the same repository.
// Exactly one must win; the rest must get ErrVersionConflict
// deterministically, never a silently corrupted row.
func TestFileRepository_ConcurrentSaveSameHost(t *testing.T) {
	ctx := context.Background()
	const n = 50

	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	// Load n independent item instances of the same host, simulating n
	// readers that all saw the row before anyone wrote to it.
	items := make([]pkginv.InventoryItem, n)
	for i := 0; i < n; i++ {
		item, err := repo.GetByName(ctx, "web-1")
		if err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
		if err := item.AddInfo("writer", fmt.Sprintf("writer-%d", i), true); err != nil {
			t.Fatalf("AddInfo %d: %v", i, err)
		}
		items[i] = item
	}

	var wg sync.WaitGroup
	// startingGate holds every goroutine at the gate so they all call Save
	// at nearly the same moment, maximizing the chance of a real race
	// rather than an accidentally-serialized one.
	var startingGate sync.WaitGroup
	startingGate.Add(1)

	var successCount int32
	var conflictCount int32
	var unexpectedErrors int32

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(item pkginv.InventoryItem) {
			defer wg.Done()
			startingGate.Wait()

			err := repo.Save(ctx, item)
			switch {
			case err == nil:
				atomic.AddInt32(&successCount, 1)
			case errors.Is(err, inventory.ErrVersionConflict):
				atomic.AddInt32(&conflictCount, 1)
			default:
				atomic.AddInt32(&unexpectedErrors, 1)
				t.Errorf("unexpected error during thundering herd: %v", err)
			}
		}(items[i])
	}

	startingGate.Done() // release the hounds
	wg.Wait()

	if unexpectedErrors > 0 {
		t.Fatalf("%d unexpected errors during thundering herd", unexpectedErrors)
	}
	if successCount != 1 {
		t.Fatalf("expected exactly 1 goroutine to win the save, got %d. SPLIT BRAIN DETECTED!", successCount)
	}
	if conflictCount != n-1 {
		t.Fatalf("expected exactly %d ErrVersionConflict, got %d", n-1, conflictCount)
	}
}

// TestFileRepository_NoLeftoverTempFileAfterSave proves atomicWriteFile's
// temp file is always cleaned up (renamed into place, never left behind)
// after a successful Save. The inventory directory must contain exactly
// hosts.yaml and the sidecar file, nothing with a temp-file name.
func TestFileRepository_NoLeftoverTempFileAfterSave(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	loaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := loaded.AddInfo("kernel", "6.6.1", false); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatalf("Save: %v", err)
	}

	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		// atomicWriteFile names its temp files ".tmp-*"; any surviving
		// entry with that prefix means a rename failed to clean up after
		// itself.
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover temp file in inventory directory: %s", e.Name())
		}
	}
	if len(names) != 2 {
		t.Fatalf("expected exactly 2 files (hosts.yaml + sidecar) after Save, got %d: %v", len(names), names)
	}
}
