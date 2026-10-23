// Tests for Replace against real files on a real SSH session.
package remotefile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// TestReplace_KeepsTheFileWhatItWas: a file this login owns is replaced
// by a rename, keeps its mode, and leaves nothing beside it; a path that
// is not a regular file is refused untouched.
//
// The in-place branch with a real second account is proven by the build
// release gate's device, whose project files belong to root's group, and
// here through a group that does not exist (below).
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

// noSuchGroup is a group no file can be given, by any login, root
// included: chgrp to it fails, which is what Replace meets when a new
// file cannot be given the old one's group.
const noSuchGroup = "pleiades-no-such-group"

// replaceFixture writes content to a fresh file with mode in a directory
// of its own, returning the directory and the file's path.
func replaceFixture(t *testing.T, content string, mode os.FileMode) (string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return dir, p
}

// leftBeside fails t when Replace left its new file beside the target.
func leftBeside(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".pleiades.") {
			t.Errorf("Replace left %s beside the file", e.Name())
		}
	}
}

// TestReplace_CopiesInPlaceWhenANewFileCannotTakeTheGroup: when the new
// file cannot be given the old one's group, the bytes are copied into the
// existing file, which keeps everything it was, and Replace says so.
func TestReplace_CopiesInPlaceWhenANewFileCannotTakeTheGroup(t *testing.T) {
	conn := connect(t)
	dir, p := replaceFixture(t, "FROM a:1\n", 0o640)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	was, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	before.Group = noSuchGroup
	r, err := remotefile.Replace(context.Background(), conn, p, []byte("FROM a:1@sha256:00\n"), before)
	if err != nil || !r.InPlace || !r.Touched {
		t.Fatalf("Replace = %+v, %v", r, err)
	}
	got, _ := os.ReadFile(p) // #nosec G304 -- a path under this test's own directory
	now, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "FROM a:1@sha256:00\n" || !os.SameFile(was, now) {
		t.Errorf("after an in-place Replace: %q, the same file: %v", got, os.SameFile(was, now))
	}
	leftBeside(t, dir)
}

// TestReplace_RefusesAFileItCannotWriteBeforeTouchingIt: in place, a file
// this login may not write is refused before anything is truncated.
func TestReplace_RefusesAFileItCannotWriteBeforeTouchingIt(t *testing.T) {
	need.Require(t, "non-root", os.Geteuid() != 0, "root writes a file whose mode forbids it")
	conn := connect(t)
	dir, p := replaceFixture(t, "FROM a:1\n", 0o444)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	before.Group = noSuchGroup
	r, err := remotefile.Replace(context.Background(), conn, p, []byte("x"), before)
	if err == nil || r.Touched || !strings.Contains(err.Error(), "may not write the file itself") {
		t.Fatalf("Replace = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(p); string(got) != "FROM a:1\n" { // #nosec G304 -- a path under this test's own directory
		t.Errorf("the file became %q", got)
	}
	leftBeside(t, dir)
}

// TestReplace_ADirectoryItCannotWriteInLeavesTheFile: with nowhere to put
// the new content beside the file, Replace fails untouched.
func TestReplace_ADirectoryItCannotWriteInLeavesTheFile(t *testing.T) {
	need.Require(t, "non-root", os.Geteuid() != 0, "root writes into a directory whose mode forbids it")
	conn := connect(t)
	dir, p := replaceFixture(t, "FROM a:1\n", 0o644)
	before, err := remotefile.Stat(context.Background(), conn, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	r, err := remotefile.Replace(context.Background(), conn, p, []byte("x"), before)
	if err == nil || r.Touched || !strings.Contains(err.Error(), "writing beside it") {
		t.Fatalf("Replace = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(p); string(got) != "FROM a:1\n" { // #nosec G304 -- a path under this test's own directory
		t.Errorf("the file became %q", got)
	}
}

// TestReplaced_FailureSaysWhenTheFileMayHaveChanged: a failure after the
// bytes may have landed carries the caller's note and still wraps the
// cause; one before carries only the cause; no failure stays nil.
func TestReplaced_FailureSaysWhenTheFileMayHaveChanged(t *testing.T) {
	cause := errors.New("copy: broken pipe")
	got := remotefile.Replaced{InPlace: true, Touched: true}.Failure(cause, "the file may hold the new text")
	if !errors.Is(got, cause) || got.Error() != "copy: broken pipe: the file may hold the new text" {
		t.Errorf("touched = %v, want the cause and the note", got)
	}
	if got := (remotefile.Replaced{}).Failure(cause, "the file may hold the new text"); got != cause {
		t.Errorf("untouched = %v, want the cause alone", got)
	}
	if got := (remotefile.Replaced{Touched: true}).Failure(nil, "note"); got != nil {
		t.Errorf("no failure = %v, want nil", got)
	}
}
