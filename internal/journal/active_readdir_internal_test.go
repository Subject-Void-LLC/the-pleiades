// Tests of readDirIfAny: a missing directory is no entries, and anything
// else standing at its path is an error, on every system.
package journal

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadDirIfAny_TellsAMissingDirectoryFromSomethingElse(t *testing.T) {
	root := t.TempDir()
	if entries, err := readDirIfAny(filepath.Join(root, "none")); err != nil || entries != nil {
		t.Errorf("a missing directory = %v, %v; want no entries and no error", entries, err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "a"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if entries, err := readDirIfAny(filepath.Join(root, "dir")); err != nil || len(entries) != 1 {
		t.Errorf("a directory = %v, %v", entries, err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDirIfAny(filepath.Join(root, "file")); err == nil {
		t.Error("a file where the directory belongs was listed as no directory")
	}
	// A link to nothing reads as missing and stands there as something
	// else: the case Windows reports for a file, reached here too.
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "gone"), filepath.Join(root, "dangling")); err != nil {
			t.Fatal(err)
		}
		if _, err := readDirIfAny(filepath.Join(root, "dangling")); err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("a dangling link = %v, want it named as not a directory", err)
		}
	}
}
