//go:build unix

// Package engine: a project file's mode, compared on a Unix system.
package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestModeHolds_ComparesEveryPermissionBitOnUnix: a real file's mode
// matches the mode it was given and no other, so a write of a new mode is a
// change here.
func TestModeHolds_ComparesEveryPermissionBitOnUnix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o600, 0o640, 0o400} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []os.FileMode{0o600, 0o640, 0o400} {
			if got := modeHolds(info.Mode(), want); got != (mode == want) {
				t.Errorf("a %04o file holds %04o = %v", mode, want, got)
			}
		}
	}
}
