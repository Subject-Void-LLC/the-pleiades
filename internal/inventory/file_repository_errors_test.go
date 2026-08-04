package inventory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// sidecarPathFor performs one successful Save against repo/hostsPath so
// the sidecar file gets created, then returns its path by finding the one
// directory entry that is not hosts.yaml. Tests that need to corrupt the
// sidecar file directly use this instead of hardcoding the sidecar's
// filename (an unexported constant in file_repository_state.go), so they
// stay correct even if that name ever changes.
func sidecarPathFor(t *testing.T, repo inventory.Repository, hostsPath, hostName string) string {
	t.Helper()
	ctx := context.Background()

	item, err := repo.GetByName(ctx, hostName)
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := item.AddInfo("bootstrap", "true", true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("bootstrap Save: %v", err)
	}

	dir := filepath.Dir(hostsPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Join(dir, e.Name()) != hostsPath {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatal("no sidecar file found after Save")
	return ""
}

// TestFileRepository_GetByName_HostNotFound covers the "no host with this
// name" branch of GetByName.
func TestFileRepository_GetByName_HostNotFound(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	if _, err := repo.GetByName(ctx, "does-not-exist"); err == nil {
		t.Fatal("expected an error for a host that does not exist")
	}
}

// TestFileRepository_MissingHostsFile_Errors covers ReadHosts's error
// propagation through both GetGroup and GetByName when hosts.yaml has
// never been written.
func TestFileRepository_MissingHostsFile_Errors(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml") // deliberately never written
	repo := inventory.NewFileRepository(path, inventory.NewItemFactory())

	if _, err := repo.GetGroup(ctx, "all"); err == nil {
		t.Error("expected GetGroup to error when hosts.yaml does not exist")
	}
	if _, err := repo.GetByName(ctx, "anything"); err == nil {
		t.Error("expected GetByName to error when hosts.yaml does not exist")
	}
}

// TestFileRepository_UnsupportedDeviceType_Errors covers the
// factory.Build error branch in both GetGroup and GetByName.
func TestFileRepository_UnsupportedDeviceType_Errors(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHosts(t, path, []inventory.HostSpec{
		{ID: "id-1", Name: "mystery-1", Type: "not_a_real_type"},
	})

	if _, err := repo.GetGroup(ctx, "all"); err == nil {
		t.Error("expected GetGroup to error building an unsupported device type")
	}
	if _, err := repo.GetByName(ctx, "mystery-1"); err == nil {
		t.Error("expected GetByName to error building an unsupported device type")
	}
}

// TestFileRepository_HostWithoutID_FallsBackToName covers the ID-fallback
// branch shared by buildRecord and findHostIndex: a hand-edited hosts.yaml
// entry with no id falls back to Name, the same fallback HydrateHosts
// (yaml_plugin.go) documents for the read-only StaticYAMLPlugin path. This
// proves the fallback also works end to end through a real Save.
func TestFileRepository_HostWithoutID_FallsBackToName(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHosts(t, path, []inventory.HostSpec{
		{Name: "no-id-host", Type: "linux_server"},
	})

	item, err := repo.GetByName(ctx, "no-id-host")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got := item.ID(); got != "no-id-host" {
		t.Errorf("ID = %q, want the Name fallback %q", got, "no-id-host")
	}

	if err := item.AddInfo("k", "v", true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := repo.GetByName(ctx, "no-id-host")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Version(); got != 1 {
		t.Errorf("version after save = %d, want 1", got)
	}
}

// TestFileRepository_Save_UnknownHost_Errors covers Save's hostIdx == -1
// branch: the loaded item's host was removed from hosts.yaml after load,
// so Save has no row left to match against.
func TestFileRepository_Save_UnknownHost_Errors(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	item, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := item.AddInfo("k", "v", true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}

	// Remove the host from hosts.yaml out from under the loaded item.
	seedHosts(t, path, nil)

	if err := repo.Save(ctx, item); err == nil {
		t.Fatal("expected Save to error when its host no longer exists in hosts.yaml")
	}
}

// TestFileRepository_Save_HostsFileDisappears_Errors covers Save's own
// ReadHosts error branch (distinct from GetByName's): hosts.yaml vanishes
// between load and Save.
func TestFileRepository_Save_HostsFileDisappears_Errors(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	item, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := item.AddInfo("k", "v", true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing hosts.yaml: %v", err)
	}

	if err := repo.Save(ctx, item); err == nil {
		t.Fatal("expected Save to error when hosts.yaml has disappeared")
	}
}

// TestFileRepository_MalformedSidecar_Errors covers readSidecar's
// unmarshal-error branch, exercised through GetGroup and GetByName.
func TestFileRepository_MalformedSidecar_Errors(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	sidecar := sidecarPathFor(t, repo, path, "web-1")
	if err := os.WriteFile(sidecar, []byte(": not valid yaml :: [["), 0o644); err != nil {
		t.Fatalf("corrupting sidecar: %v", err)
	}

	if _, err := repo.GetGroup(ctx, "all"); err == nil {
		t.Error("expected GetGroup to error on a malformed sidecar file")
	}
	if _, err := repo.GetByName(ctx, "web-1"); err == nil {
		t.Error("expected GetByName to error on a malformed sidecar file")
	}
}

// TestFileRepository_Save_MalformedSidecar_Errors covers Save's own
// readSidecar error branch (distinct from GetGroup/GetByName's): the
// sidecar becomes unreadable between load and Save.
func TestFileRepository_Save_MalformedSidecar_Errors(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	sidecar := sidecarPathFor(t, repo, path, "web-1")

	item, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := item.AddInfo("k2", "v2", true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}

	if err := os.WriteFile(sidecar, []byte(": not valid yaml :: (("), 0o644); err != nil {
		t.Fatalf("corrupting sidecar: %v", err)
	}

	if err := repo.Save(ctx, item); err == nil {
		t.Fatal("expected Save to error on a malformed sidecar file")
	}
}

// TestFileRepository_UnrecognizedSidecarState_Errors covers buildRecord's
// ParseLifecycleState error branch, mirroring
// TestGetByName_RejectsUnknownStoredState (ent_save_test.go): an
// unrecognized stored state must fail loudly, never be silently promoted
// to StateActive, since Active is the one state that permits execution.
func TestFileRepository_UnrecognizedSidecarState_Errors(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	sidecar := sidecarPathFor(t, repo, path, "web-1")
	// Hand-write a sidecar entry with a lifecycle state this build does
	// not recognize. See sidecarEntry's doc comment (file_repository_state.go)
	// for the on-disk shape this mirrors.
	content := "hosts:\n  - id: id-1\n    version: 1\n    state: a-state-from-a-newer-build\n"
	if err := os.WriteFile(sidecar, []byte(content), 0o644); err != nil {
		t.Fatalf("corrupting sidecar: %v", err)
	}

	if _, err := repo.GetGroup(ctx, "all"); err == nil {
		t.Error("expected GetGroup to reject an unrecognized stored state")
	}
	if _, err := repo.GetByName(ctx, "web-1"); err == nil {
		t.Error("expected GetByName to reject an unrecognized stored state")
	}
}

// TestFileRepository_BuildRecordUsesStoredSource is the regression test
// for the chain audit's provenance finding (IMPLEMENTATION.md Phase W4):
// buildRecord used to hardcode every file-backed device to
// Source.Plugin "file", ignoring anything actually recorded in the
// sidecar. It hand-writes a sidecar entry carrying a different plugin
// name and a sync timestamp, exactly the shape a future reconciling sync
// plugin (Phase 6) would produce, and asserts GetByName reports it rather
// than the hardcoded default.
func TestFileRepository_BuildRecordUsesStoredSource(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	sidecar := sidecarPathFor(t, repo, path, "web-1")
	content := "hosts:\n  - id: id-1\n    version: 1\n    state: active\n    source: netbox\n    source_synced_at: 2026-01-15T10:00:00Z\n"
	if err := os.WriteFile(sidecar, []byte(content), 0o644); err != nil {
		t.Fatalf("writing sidecar: %v", err)
	}

	item, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got := item.Source().Plugin; got != "netbox" {
		t.Errorf("Source().Plugin = %q, want %q (buildRecord must not hardcode \"file\")", got, "netbox")
	}
	if item.Source().SyncedAt.IsZero() {
		t.Error("Source().SyncedAt = zero, want the stored sidecar timestamp")
	}

	// Save must not silently revert the source it just loaded back to the
	// "file" default: the point of persisting it is that it survives a
	// write, not only a read.
	if err := item.AddInfo("checked", "true", true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName after save: %v", err)
	}
	if got := reloaded.Source().Plugin; got != "netbox" {
		t.Errorf("Source().Plugin after Save = %q, want %q", got, "netbox")
	}
}

// TestFileRepository_GetGroup_HonorsContextCancellation covers
// fileIterator.Next's ctx.Err() short-circuit: a caller that already gave
// up must see false rather than another item, even though iterating an
// already-loaded in-memory slice costs nothing on its own.
func TestFileRepository_GetGroup_HonorsContextCancellation(t *testing.T) {
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	iter, err := repo.GetGroup(context.Background(), "all")
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

// TestFileRepository_Save_UnmarshalableRevisionValue_Errors covers
// writeSidecar's yaml.Marshal error branch, propagated through Save. A
// channel cannot be YAML-marshaled; AddInfo accepts any PropertyValue
// (pkg/inventory/item.go's alias for any), so nothing at the type level
// stops a caller from recording one. Save must fail cleanly rather than
// panic or silently drop the value, and since writeSidecar runs before
// writeHostsAtomic (see Save's doc comment for why that ordering is the
// safe one), hosts.yaml must be left completely untouched.
func TestFileRepository_Save_UnmarshalableRevisionValue_Errors(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

	item, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := item.AddInfo("bad", make(chan int), true); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}

	if err := repo.Save(ctx, item); err == nil {
		t.Fatal("expected Save to error when a revision value cannot be marshaled to YAML")
	}

	// hosts.yaml must be unaffected: the sidecar write failed first, so
	// Save must never have reached the hosts.yaml write.
	reloaded, err := repo.GetByName(ctx, "web-1")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.Version(); got != 0 {
		t.Errorf("version = %d, want 0: a failed Save must not have partially landed", got)
	}
}

// TestFileRepository_Save_NoOpWhenNothingChanged mirrors
// TestSave_NoOpWhenNothingChanged (ent_save_test.go): guards against
// burning a version on a save that has nothing to record, and proves a
// no-op Save does not even create the sidecar file, since it returns
// before ever reaching the write path.
func TestFileRepository_Save_NoOpWhenNothingChanged(t *testing.T) {
	ctx := context.Background()
	repo, path := newTestFileRepo(t)
	seedHost(t, path, "id-1", "web-1")

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

	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only hosts.yaml to exist after a no-op Save, got %d entries", len(entries))
	}
}
