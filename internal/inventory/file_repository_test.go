package inventory_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	pkginventory "github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// newTestFileRepo creates a Repository backed by a hosts.yaml file inside
// a fresh t.TempDir(). Per RULE 0 these tests exercise a real filesystem,
// not a mock or an in-memory fake: the whole point of this code is that a
// value survives a round trip through actual files, which a fake
// filesystem cannot demonstrate.
func newTestFileRepo(t *testing.T) (inventory.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	return inventory.NewFileRepository(path, inventory.NewItemFactory()), path
}

// seedHosts writes hosts to path as a static YAML inventory file.
func seedHosts(t *testing.T, path string, hosts []inventory.HostSpec) {
	t.Helper()
	if err := inventory.WriteHosts(path, hosts); err != nil {
		t.Fatalf("seeding hosts: %v", err)
	}
}

// seedHost seeds a single linux_server host with a given id and name.
func seedHost(t *testing.T, path, id, name string) {
	t.Helper()
	seedHosts(t, path, []inventory.HostSpec{
		{
			ID:   id,
			Name: name,
			Type: "linux_server",
			Properties: map[string]interface{}{
				"host": "10.0.0.9",
			},
		},
	})
}

// TestFileRepository_RoundTripsPropertiesVersionAndHistory mirrors
// TestSave_RoundTripsPropertiesVersionAndHistory (ent_save_test.go) for
// the file-backed repository: both implementations sit behind the same
// Repository port and must behave identically to callers.
func TestFileRepository_RoundTripsPropertiesVersionAndHistory(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	loaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got := loaded.Version(); got != 0 {
		t.Fatalf("fresh host version = %d, want 0", got)
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

	// The property must have survived, through hosts.yaml.
	if got, _ := reloaded.Properties().String("kernel"); got != "6.6.2" {
		t.Errorf("kernel after reload = %q, want %q", got, "6.6.2")
	}

	// The version must have survived, through the sidecar.
	if got := reloaded.Version(); got != 2 {
		t.Errorf("version after reload = %d, want 2", got)
	}

	// The audit trail must have survived, in order, with old and new
	// values, also through the sidecar.
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

// TestFileRepository_RejectsConcurrentWriteAndPreservesFirstWriter mirrors
// TestSave_RejectsConcurrentWriteAndPreservesFirstWriter (ent_save_test.go)
// and proves Save is a compare-and-swap rather than last-write-wins: the
// second writer must fail AND must not have overwritten the first
// writer's value.
//
// Mutation tested per LESSONS_LEARNED.md entry 20: the conflict check this
// test guards is file_repository_save.go's "if entry.Version != baseVersion"
// re-read comparison in Save. That line was temporarily changed to
// "if false && entry.Version != baseVersion" (never detect a conflict) and
// this test was re-run; it failed with "second Save error = <nil>, want
// ErrVersionConflict", proving the check is load-bearing rather than
// decorative. The line was then restored to its original form before this
// file was committed, and this test passes again against the restored code.
func TestFileRepository_RejectsConcurrentWriteAndPreservesFirstWriter(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

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

// TestFileRepository_GetGroupListViewOmitsHistory proves GetGroup's items
// carry Version but not History, contrasted directly against GetByName's
// fully hydrated result in the same test, as a regression check on the
// Repository interface's documented list-view contract (iterator.go).
func TestFileRepository_GetGroupListViewOmitsHistory(t *testing.T) {
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

	iter, err := repo.GetGroup(ctx, pkginventory.Selector{})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	defer iter.Close()

	if !iter.Next(ctx) {
		t.Fatalf("expected at least one item, iterator error: %v", iter.Error())
	}
	listItem := iter.Item()
	if got := listItem.Version(); got != 1 {
		t.Errorf("list view version = %d, want 1", got)
	}
	if got := len(listItem.History()); got != 0 {
		t.Errorf("list view history length = %d, want 0 (a list view must not load history)", got)
	}
	if iter.Next(ctx) {
		t.Errorf("expected exactly one item from GetGroup")
	}
	if err := iter.Error(); err != nil {
		t.Errorf("iterator error: %v", err)
	}

	// Contrast against GetByName, which must be fully hydrated.
	full, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got := len(full.History()); got != 1 {
		t.Errorf("GetByName history length = %d, want 1", got)
	}
}

// TestFileRepository_ResolvesClassifyOnlyHost proves classification is
// wired into the real production hydration path, not only HydrateHosts
// (which cmd/pleiades's actual CLI commands never call): GetGroup and
// GetByName both go through buildRecord, which is what pleiades validate
// and pleiades run actually use.
func TestFileRepository_ResolvesClassifyOnlyHost(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHosts(t, path, []inventory.HostSpec{
		{
			ID:       "id-1",
			Name:     "web-1",
			Classify: []string{"linux_server", "debian_family", "ubuntu"},
			Properties: map[string]interface{}{
				"host": "10.0.0.9",
			},
		},
	})

	item, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName on a classify-only host: %v", err)
	}
	if !item.HasCapability(capability.NameLinux) {
		t.Error("expected a linux_server-classified host to declare LinuxCapable")
	}

	iter, err := repo.GetGroup(ctx, pkginventory.Selector{})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	defer iter.Close()
	if !iter.Next(ctx) {
		t.Fatalf("expected at least one item from GetGroup, iterator error: %v", iter.Error())
	}
	if !iter.Item().HasCapability(capability.NameLinux) {
		t.Error("GetGroup's list view did not resolve Classify the same way GetByName did")
	}
}

// The concurrent-Save race tests (different hosts, same host, and the
// no-leftover-temp-file check) live in file_repository_concurrency_test.go,
// a sibling file kept separate so this file stays focused on Save's
// single-writer correctness contract.

// TestFileRepository_Selector_GroupNameIgnored pins down a deliberate,
// documented gap (GetGroup's own doc comment): Walk tier's HostSpec has no
// group-membership field at all, unlike the ent-backed adapter, which now
// pushes sel.GroupName down to SQL via a real Group edge. A non-empty
// GroupName here must still return every host rather than erroring or
// silently filtering to nothing, so a future change cannot quietly start
// erroring on it without this test forcing that decision to be visible.
func TestFileRepository_Selector_GroupNameIgnored(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHosts(t, path, []inventory.HostSpec{
		{ID: "id-1", Name: "web-1", Type: "linux_server", Properties: map[string]interface{}{"host": "10.0.0.1"}},
		{ID: "id-2", Name: "web-2", Type: "linux_server", Properties: map[string]interface{}{"host": "10.0.0.2"}},
	})

	iter, err := repo.GetGroup(ctx, pkginventory.Selector{GroupName: "prod"})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	defer iter.Close()

	count := 0
	for iter.Next(ctx) {
		count++
	}
	if err := iter.Error(); err != nil {
		t.Fatalf("iterator error: %v", err)
	}
	if count != 2 {
		t.Errorf("GetGroup with a non-empty GroupName the file backend cannot honor returned %d hosts, want 2 (both, unfiltered)", count)
	}
}
