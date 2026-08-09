package ent_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	_ "github.com/mattn/go-sqlite3"
)

// TestOpenEmbeddedDurability proves the one claim client_test.go's
// in-memory ("file:ent?mode=memory&cache=shared") setup structurally cannot
// prove: that data survives a full process restart against a real on-disk
// file. RULE 0 requires this be a real file, not :memory:, and not a
// t.TempDir() that gets abandoned rather than reopened.
func TestOpenEmbeddedDurability(t *testing.T) {
	ctx := context.Background()
	// A real on-disk path inside the test's temp directory. t.TempDir() is
	// still a real filesystem location, unlike SQLite's :memory: or shared
	// cache DSNs, so closing and reopening it is a genuine durability test.
	path := filepath.Join(t.TempDir(), "pleiades.db")

	client, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatalf("first OpenEmbedded failed: %v", err)
	}

	created, err := client.Device.Create().
		SetName("core-sw-01").
		SetType("network_device").
		SetProperties(map[string]interface{}{"host": "10.0.0.1"}).
		Save(ctx)
	if err != nil {
		client.Close()
		t.Fatalf("failed creating device: %v", err)
	}

	// While the client is still open, WAL mode should have produced its
	// side files next to the main database file. This is an
	// externally-observable proof that the "_journal_mode=WAL" DSN
	// parameter actually took effect, not just that it was spelled
	// correctly in the string.
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Errorf("expected WAL side file %q to exist while database is open: %v", path+"-wal", err)
	}

	// Close the client. This is the "process restart" boundary: everything
	// in-memory (the *ent.Client, its connection pool, any cache) is gone
	// after this point, and only what is on disk remains.
	if err := client.Close(); err != nil {
		t.Fatalf("failed closing first client: %v", err)
	}

	// Reopen the SAME on-disk file as a brand new client. This is the
	// actual durability assertion: a fresh OpenEmbedded call, independent
	// of the first client's in-memory state, must see the row that was
	// written before the restart.
	reopened, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatalf("second OpenEmbedded (reopen) failed: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.Device.Query().Where(device.NameEQ("core-sw-01")).Only(ctx)
	if err != nil {
		t.Fatalf("device did not survive reopen: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("expected reopened device id %d, got %d", created.ID, got.ID)
	}

	// Migration ran again as part of the second OpenEmbedded call above. If
	// it were not idempotent, that call would already have failed. Confirm
	// it also did not duplicate rows or tables: exactly one device must be
	// present, not two.
	count, err := reopened.Device.Query().Count(ctx)
	if err != nil {
		t.Fatalf("failed counting devices after reopen: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 device after reopen (proving migration did not duplicate schema/data), got %d", count)
	}
}

// TestOpenEmbeddedCreatesParentDirectory proves the os.MkdirAll step: a
// freshly scaffolded Walk-tier project directory will not already contain
// the nested directory an embedded database file lives in, so OpenEmbedded
// must create it rather than failing with "no such file or directory".
func TestOpenEmbeddedCreatesParentDirectory(t *testing.T) {
	ctx := context.Background()
	// Several levels deep, none of which exist yet under t.TempDir().
	path := filepath.Join(t.TempDir(), "state", "nested", "deeper", "pleiades.db")

	client, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatalf("OpenEmbedded failed to create missing parent directories: %v", err)
	}
	defer client.Close()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected database file to exist at %q: %v", path, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || !info.IsDir() {
		t.Errorf("expected parent directory %q to have been created", filepath.Dir(path))
	}
}

// TestOpenEmbeddedParentDirectoryCreationFails proves the MkdirAll error is
// actually wrapped and surfaced, not swallowed, when the parent directory
// cannot be created. This is forced by pointing the requested parent at a
// path that already exists as a plain file: os.MkdirAll cannot create a
// directory on top of an existing file.
func TestOpenEmbeddedParentDirectoryCreationFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Create a plain file where OpenEmbedded will try to create a
	// directory.
	blocker := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed writing blocker file: %v", err)
	}

	// The requested database lives inside a "directory" that is actually
	// the blocker file, so MkdirAll must fail.
	path := filepath.Join(blocker, "subdir", "pleiades.db")

	client, err := ent.OpenEmbedded(ctx, path)
	if err == nil {
		client.Close()
		t.Fatalf("expected OpenEmbedded to fail when the parent directory cannot be created, got a usable client")
	}
	if client != nil {
		t.Errorf("expected a nil client alongside a non-nil error, got %v", client)
	}
}

// TestOpenEmbeddedSequentialOpenCloseCycles proves that repeated
// open-write-close-reopen cycles against the same file do not corrupt it.
// This matters because WAL mode keeps a "-wal" side file that is only fully
// folded back into the main database file on checkpoint or clean close; a
// bug in that path would show up as data loss or corruption on the second
// or third cycle, not necessarily the first.
func TestOpenEmbeddedSequentialOpenCloseCycles(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cycles", "pleiades.db")

	// Cycle 1: open, write, close.
	first, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatalf("cycle 1 open failed: %v", err)
	}
	if _, err := first.Device.Create().SetName("device-one").SetType("linux_server").Save(ctx); err != nil {
		first.Close()
		t.Fatalf("cycle 1 write failed: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("cycle 1 close failed: %v", err)
	}

	// Cycle 2: reopen, write a second row, close.
	second, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatalf("cycle 2 open failed: %v", err)
	}
	if _, err := second.Device.Create().SetName("device-two").SetType("linux_server").Save(ctx); err != nil {
		second.Close()
		t.Fatalf("cycle 2 write failed: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("cycle 2 close failed: %v", err)
	}

	// Cycle 3: reopen read-only (no write) and confirm both prior rows
	// survived both round trips intact.
	third, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		t.Fatalf("cycle 3 open failed: %v", err)
	}
	defer third.Close()

	names, err := third.Device.Query().Order(ent.Asc(device.FieldName)).All(ctx)
	if err != nil {
		t.Fatalf("failed querying after 3 cycles: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 devices surviving 3 open/close cycles, got %d", len(names))
	}
	if names[0].Name != "device-one" || names[1].Name != "device-two" {
		t.Errorf("unexpected device names after cycling: got %q, %q", names[0].Name, names[1].Name)
	}
}
