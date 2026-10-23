//go:build !unix

// Package engine: a project file's mode, compared where modes are not Unix
// ones (Windows).
package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// TestModeHolds_ComparesOnlyTheReadOnlyBit: a file written 0600 reads back
// 0666 here and still holds 0600, so writing it again is no change (the
// Unix comparison called every run a change); a read-only file holds a
// read-only mode and not a writable one.
func TestModeHolds_ComparesOnlyTheReadOnlyBit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !modeHolds(info.Mode(), 0o600) || !modeHolds(info.Mode(), 0o640) || modeHolds(info.Mode(), 0o400) {
		t.Errorf("a writable file (%04o here): holds 0600 %v, 0640 %v, 0400 %v; want true, true, false",
			info.Mode().Perm(), modeHolds(info.Mode(), 0o600), modeHolds(info.Mode(), 0o640), modeHolds(info.Mode(), 0o400))
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if info, err = os.Lstat(path); err != nil {
		t.Fatal(err)
	}
	if !modeHolds(info.Mode(), 0o400) || modeHolds(info.Mode(), 0o600) {
		t.Errorf("a read-only file (%04o here): holds 0400 %v, 0600 %v; want true, false",
			info.Mode().Perm(), modeHolds(info.Mode(), 0o400), modeHolds(info.Mode(), 0o600))
	}
}
