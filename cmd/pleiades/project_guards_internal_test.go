// Tests of the paths a run's project writes are kept from.
package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestProjectWriteGuards_NameTheInventoryAndTheCollectionsDirectory: the
// guards are the inventory, its state file and, when one is set, the
// external Collections directory, each absolute against the working
// directory, as --dir and the environment variable are read; with none set
// only the inventory's two files are guarded.
func TestProjectWriteGuards_NameTheInventoryAndTheCollectionsDirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(collectionsDirEnv, "")
	got := projectWriteGuards("proj")
	want := []string{filepath.Join(wd, "proj", "inventory.yaml"), filepath.Join(wd, "proj", ".inventory-state.generated.yaml")}
	if !slices.Equal(got, want) {
		t.Errorf("with no collections directory: %v, want %v", got, want)
	}
	t.Setenv(collectionsDirEnv, filepath.Join("proj", "collections"))
	got = projectWriteGuards("proj")
	if !slices.Equal(got, append(want, filepath.Join(wd, "proj", "collections"))) {
		t.Errorf("with a collections directory: %v", got)
	}
}
