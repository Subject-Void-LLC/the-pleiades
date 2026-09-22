// Tests for ensureSQLiteWAL (open_sqlite.go), which creates a new SQLite file
// privately, already in WAL mode, and publishes it with a hard link, so two
// processes opening one new database together cannot race on the mode switch
// (FAILURE_PATTERNS.md #277). The many-process proof is open_race_test.go.
package ent

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// TestEnsureSQLiteWAL_PublishesANewFileAlreadyInWAL proves what a new
// database looks like the moment it exists: in WAL mode, readable by its
// owner only, and with nothing left behind under a temporary name.
func TestEnsureSQLiteWAL_PublishesANewFileAlreadyInWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.db")

	if err := ensureSQLiteWAL(path); err != nil {
		t.Fatalf("ensureSQLiteWAL() = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the database was not created: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the new database has mode %o; want 0600", mode)
	}
	if got := journalMode(t, path); got != "wal" {
		t.Errorf("the new database is in %q mode; want wal", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pleiades-new-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// TestEnsureSQLiteWAL_LeavesAnExistingFileAlone proves an existing database
// is neither replaced nor rewritten: publishing is only for a name nobody has
// taken yet.
func TestEnsureSQLiteWAL_LeavesAnExistingFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(path, []byte("not touched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureSQLiteWAL(path); err != nil {
		t.Fatalf("ensureSQLiteWAL() = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "not touched" {
		t.Errorf("an existing file was rewritten: %q", got)
	}
}

// TestEnsureSQLiteWAL_WithoutHardLinksStillCreatesAPrivateFile is
// FAILURE_PATTERNS.md #289: on a filesystem that cannot hard link, the
// fallback used to leave creation to the driver, whose file follows the
// umask. It must still be 0600, still a database SQLite opens, and nothing
// may be left behind under a temporary name.
func TestEnsureSQLiteWAL_WithoutHardLinksStillCreatesAPrivateFile(t *testing.T) {
	saved := hardLink
	t.Cleanup(func() { hardLink = saved })
	hardLink = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.EPERM} }

	dir := t.TempDir()
	path := filepath.Join(dir, "new.db")
	if err := ensureSQLiteWAL(path); err != nil {
		t.Fatalf("ensureSQLiteWAL() with no hard links = %v; want the fallback to create the file", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the fallback created no database: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the fallback's database has mode %o; want 0600", mode)
	}
	// An empty file is a database SQLite accepts, and the DSN's own WAL
	// request switches it on first open.
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE t (id integer)"); err != nil {
		t.Errorf("SQLite would not use the fallback's file: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pleiades-new-") {
			t.Errorf("the fallback left %s behind", e.Name())
		}
	}

	// A second process racing the first finds the name taken, which is as
	// good as creating it, and must not fail.
	if err := os.Remove(path + "-wal"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := ensureSQLiteWAL(path); err != nil {
		t.Errorf("ensureSQLiteWAL() on a name another process created = %v; want nil", err)
	}
}

// journalMode reads a SQLite file's journal mode through a fresh connection
// that asks for nothing, so the answer is what the file records.
func journalMode(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	return strings.ToLower(mode)
}
