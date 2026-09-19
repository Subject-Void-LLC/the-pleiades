// Package main: that nothing is loaded, refused or printed when
// PLEIADES_COLLECTIONS_DIR is unset.
package main

import (
	"context"
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
)

// TestLoadExternalCollections_UnsetIsSilent covers the other half of the
// platform decision: with PLEIADES_COLLECTIONS_DIR unset, the loader is
// never reached, so no platform (Windows included, where it would refuse)
// refuses anything or prints a word. Only a user who asked for external
// Collections hears about them.
func TestLoadExternalCollections_UnsetIsSilent(t *testing.T) {
	t.Setenv(collectionsDirEnv, "")
	if err := os.Unsetenv(collectionsDirEnv); err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = w
	var set *loader.Set
	var loadErr error
	stdout := captureStdout(t, func() {
		set, loadErr = loadExternalCollections(context.Background(), t.TempDir())
	})
	os.Stderr = original
	_ = w.Close()
	n, _ := r.Read(make([]byte, 1))
	_ = r.Close()

	if set != nil || loadErr != nil {
		t.Errorf("loadExternalCollections with the variable unset = %v, %v; want nothing loaded and nothing refused", set, loadErr)
	}
	if stdout != "" || n != 0 {
		t.Errorf("printed with the variable unset: stdout %q, stderr %d byte(s)", stdout, n)
	}
}
