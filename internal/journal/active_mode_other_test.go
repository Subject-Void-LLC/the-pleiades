//go:build !unix

// Package journal: where modes are not Unix ones (Windows), an active-run
// record's mode says nothing about who may write it, so it decides nothing.
package journal

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOthersMayWrite_IgnoresAModeThisSystemDoesNotKeep: a record written
// as BeginActive writes it (0600) reads back as 0666 on Windows, and must
// still be believed: the Unix test skipped every such record, so a live
// run was never seen and a dead run's record was never removed.
func TestOthersMayWrite_IgnoresAModeThisSystemDoesNotKeep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if othersMayWrite(info) {
		t.Errorf("a record this run wrote (mode %04o here) was taken for one others may write", info.Mode().Perm())
	}
}
