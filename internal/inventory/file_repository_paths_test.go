// Tests that FileRepositoryPaths names the files a file-backed inventory
// really reads and writes.
package inventory

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestFileRepositoryPaths_AreTheFilesTheRepositoryWrites: after a save,
// the inventory directory holds exactly the two files FileRepositoryPaths
// names, so a guard built from them covers what the repository keeps.
func TestFileRepositoryPaths_AreTheFilesTheRepositoryWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultInventoryFilename)
	if err := os.WriteFile(path, []byte("hosts:\n  - id: h1\n    name: web1\n    type: linux_server\n    properties: {host: 10.0.0.2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := NewFileRepository(path, NewItemFactory())
	item, err := repo.GetByName(context.Background(), "web1")
	if err != nil {
		t.Fatal(err)
	}
	// A change, so the save records a version in the state file.
	if err := item.AddInfo("site", "east", true); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), item); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var written []string
	for _, e := range entries {
		written = append(written, filepath.Join(dir, e.Name()))
	}
	want := FileRepositoryPaths(path)
	slices.Sort(written)
	slices.Sort(want)
	if !slices.Equal(written, want) {
		t.Errorf("the repository keeps %v; FileRepositoryPaths names %v", written, want)
	}
}
