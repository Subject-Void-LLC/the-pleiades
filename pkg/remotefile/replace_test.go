// Tests for Replace against real files on a real SSH session.
package remotefile_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// TestReplace_KeepsTheFileWhatItWas: a file this login owns is replaced
// by a rename, keeps its mode, and leaves nothing beside it; a path that
// is not a regular file is refused untouched.
//
// The in-place branch (a group this login cannot give a new file) needs a
// second account, so it is proven where one exists: the build release
// gate's device, whose project files belong to root's group.
func TestReplace_KeepsTheFileWhatItWas(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(p, []byte("FROM a:1\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := remotefile.Replace(context.Background(), conn, p, []byte("FROM a:1@sha256:00\n"), before)
	if err != nil || r.InPlace || !r.Touched {
		t.Fatalf("Replace = %+v, %v", r, err)
	}
	got, _ := os.ReadFile(p) // #nosec G304 -- a path under this test's own directory
	info, _ := os.Stat(p)
	if string(got) != "FROM a:1@sha256:00\n" || info.Mode().Perm() != 0o640 {
		t.Fatalf("after Replace: %q, mode %v", got, info.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pleiades.") {
			t.Errorf("Replace left %s beside the file", e.Name())
		}
	}

	dirInfo, err := remotefile.Stat(context.Background(), conn, dir)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := remotefile.Replace(context.Background(), conn, dir, []byte("x"), dirInfo); err == nil || r.Touched || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Replace of a directory = %+v, %v", r, err)
	}
}
