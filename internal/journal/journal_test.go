// Package journal_test: the file store's construction and permissions.
//
// Everything here reads the real filesystem. The subject is what lands on
// disk and who can read it, and a stand-in filesystem would only prove
// the stand-in agreed with itself.
package journal_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// journalDir is where a store rooted at root keeps its files.
func journalDir(root string) string {
	return filepath.Join(root, ".pleiades", "journal")
}

func TestNewFileStoreCreatesBothDirectoriesOwnerOnly(t *testing.T) {
	root := t.TempDir()
	store, err := journal.NewFileStore(root)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if got, want := store.Dir(), journalDir(root); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}

	for _, path := range []string{filepath.Join(root, ".pleiades"), journalDir(root)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s has mode %04o, want 0700", path, got)
		}
	}
}

func TestNewFileStoreTightensADirectoryItDidNotCreate(t *testing.T) {
	// os.MkdirAll applies its mode only to directories it actually
	// creates, so a .pleiades left behind by an earlier run or a
	// permissive umask keeps whatever mode it had. This is the whole
	// reason for the explicit Chmod, and the case that proves it needs a
	// directory that already exists and is loose.
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	root := t.TempDir()
	loose := filepath.Join(root, ".pleiades")
	if err := os.MkdirAll(loose, 0o755); err != nil {
		t.Fatalf("seeding a loose directory: %v", err)
	}
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatalf("loosening the directory: %v", err)
	}

	if _, err := journal.NewFileStore(root); err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	info, err := os.Stat(loose)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("%s kept mode %04o, want it tightened to 0700", loose, got)
	}
}

func TestNewFileStoreFailsWhenItCannotCreateItsDirectory(t *testing.T) {
	// A journal the operator cannot write is one that silently records
	// nothing, and the moment to find that out is before a run starts
	// changing devices.
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test depends on")
	}
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o500); err != nil {
		t.Fatalf("seeding a read-only project directory: %v", err)
	}
	t.Cleanup(func() {
		// Restored so t.TempDir's own cleanup can remove it.
		if err := os.Chmod(root, 0o700); err != nil {
			t.Fatalf("restoring the project directory: %v", err)
		}
	})

	if _, err := journal.NewFileStore(root); err == nil {
		t.Error("NewFileStore reported success under a project directory it cannot create into")
	}
}

func TestNewFileStoreFailsWhenTheDirectoryNameIsTakenByAFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pleiades"), 0o700); err != nil {
		t.Fatalf("seeding .pleiades: %v", err)
	}
	if err := os.WriteFile(journalDir(root), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seeding a file where the directory belongs: %v", err)
	}
	if _, err := journal.NewFileStore(root); err == nil {
		t.Error("NewFileStore reported success with a file sitting where its directory belongs")
	}
}

func TestNewFileStoreRestoresTheWriteBitOnItsOwnDirectory(t *testing.T) {
	// The Chmod normalizes in both directions, which is easy to read as
	// tightening only. These two directories belong to this tool and
	// 0o700 is the one mode they are supposed to have, so a store that
	// accepted 0o500 would be one that cannot write its own journal.
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply on windows")
	}
	root := t.TempDir()
	dir := journalDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("seeding the directory: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}

	if _, err := journal.NewFileStore(root); err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("%s kept mode %04o, want 0700", dir, got)
	}
}

func TestNewFileStoreLeavesNoProbeBehind(t *testing.T) {
	root := t.TempDir()
	if _, err := journal.NewFileStore(root); err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	entries, err := os.ReadDir(journalDir(root))
	if err != nil {
		t.Fatalf("reading the journal directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the journal directory holds %d files after construction, want 0: %v", len(entries), entries)
	}
}

func TestNewFileStoreIsSafeToCallTwice(t *testing.T) {
	// A second run against the same project directory is the ordinary
	// case, not an edge case.
	root := t.TempDir()
	if _, err := journal.NewFileStore(root); err != nil {
		t.Fatalf("first NewFileStore: %v", err)
	}
	if _, err := journal.NewFileStore(root); err != nil {
		t.Fatalf("second NewFileStore: %v", err)
	}
}
