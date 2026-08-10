package legacy_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/legacy"
)

func TestDirPlaybookSource_ResolvesYmlAndYaml(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "upgrade.yml"), []byte("---\n- hosts: all\n"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "backup.yaml"), []byte("---\n- hosts: all\n  tasks: []\n"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	src, err := legacy.NewDirPlaybookSource(dir)
	if err != nil {
		t.Fatalf("NewDirPlaybookSource returned unexpected error: %v", err)
	}

	got, err := src.Get(context.Background(), "upgrade")
	if err != nil {
		t.Fatalf("Get(\"upgrade\") returned unexpected error: %v", err)
	}
	if string(got) != "---\n- hosts: all\n" {
		t.Errorf("Get(\"upgrade\") = %q, want the .yml fixture's own bytes", got)
	}

	got, err = src.Get(context.Background(), "backup")
	if err != nil {
		t.Fatalf("Get(\"backup\") returned unexpected error: %v", err)
	}
	if string(got) != "---\n- hosts: all\n  tasks: []\n" {
		t.Errorf("Get(\"backup\") = %q, want the .yaml fixture's own bytes", got)
	}
}

func TestNewDirPlaybookSource_RejectsMissingDirectory(t *testing.T) {
	_, err := legacy.NewDirPlaybookSource(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing directory, got nil")
	}
}

func TestNewDirPlaybookSource_RejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}
	_, err := legacy.NewDirPlaybookSource(file)
	if err == nil {
		t.Fatal("expected an error for a non-directory path, got nil")
	}
}

func TestDirPlaybookSource_UnknownIDReturnsErrPlaybookNotFound(t *testing.T) {
	dir := t.TempDir()
	src, err := legacy.NewDirPlaybookSource(dir)
	if err != nil {
		t.Fatalf("NewDirPlaybookSource returned unexpected error: %v", err)
	}

	_, err = src.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, legacy.ErrPlaybookNotFound) {
		t.Errorf("Get error = %v, want ErrPlaybookNotFound", err)
	}
}

// TestDirPlaybookSource_RejectsPathTraversal proves a hostile id is
// rejected before any path construction, the same trust boundary
// internal/runbook/dir_source.go's own validRunbookID closes.
func TestDirPlaybookSource_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	// A real file outside dir, so a successful traversal would have
	// something real to read: proves rejection, not a coincidental
	// ErrPlaybookNotFound from a merely-missing file.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.yml"), []byte("should never be read"), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	src, err := legacy.NewDirPlaybookSource(dir)
	if err != nil {
		t.Fatalf("NewDirPlaybookSource returned unexpected error: %v", err)
	}

	for _, hostile := range []string{
		"../" + filepath.Base(outside) + "/secret",
		"../../etc/passwd",
		"a/b",
		"a\x00b",
	} {
		if _, err := src.Get(context.Background(), hostile); err == nil {
			t.Errorf("Get(%q) succeeded, want rejection", hostile)
		} else if errors.Is(err, legacy.ErrPlaybookNotFound) {
			t.Errorf("Get(%q) failed with ErrPlaybookNotFound, want a validation rejection instead (id should never reach path construction)", hostile)
		}
	}
}
