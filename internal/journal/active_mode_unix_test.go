//go:build unix

// Package journal: on a Unix system an active-run record that another
// account may write is not believed.
package journal

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOthersMayWrite_ReadsTheGroupAndWorldWriteBits: a real file at the
// mode BeginActive writes (0600) is the run's own, and one group or others
// may write is not, whatever else its mode says.
func TestOthersMayWrite_ReadsTheGroupAndWorldWriteBits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[os.FileMode]bool{0o600: false, 0o644: false, 0o620: true, 0o602: true, 0o666: true} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := othersMayWrite(info); got != want {
			t.Errorf("othersMayWrite(%04o) = %v, want %v", mode, got, want)
		}
	}
}
