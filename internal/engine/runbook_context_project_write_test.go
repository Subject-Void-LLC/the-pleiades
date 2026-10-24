// Tests that a method writes into the project, atomically, and nowhere
// else.
package engine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestWriteProjectFile_WritesChangesAndConverges: a new file in a new
// directory is written with its mode, a second write of the same bytes
// changes nothing, new bytes or a new mode change it, a check predicts
// each without writing, and no temporary file is left behind.
func TestWriteProjectFile_WritesChangesAndConverges(t *testing.T) {
	dir := projectWith(t, nil)
	c := &runbookContext{projectDir: dir}
	path := filepath.Join(dir, "configs", "r3", "running.conf")

	if changed, err := c.WriteProjectFile("configs/r3/running.conf", []byte("a\n"), 0o600, false); err != nil || !changed {
		t.Fatalf("a check of a new file = %v, %v; want a predicted change", changed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "configs")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a check created the directory: %v", err)
	}
	if changed, err := c.WriteProjectFile("configs/r3/running.conf", []byte("a\n"), 0o600, true); err != nil || !changed {
		t.Fatalf("writing a new file = %v, %v", changed, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "a\n" {
		t.Fatalf("the file holds %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("mode %v, want 0600", info.Mode().Perm())
		}
	}
	for _, write := range []bool{false, true} {
		if changed, err := c.WriteProjectFile("configs/r3/running.conf", []byte("a\n"), 0o600, write); err != nil || changed {
			t.Errorf("the same bytes again (write %v) = %v, %v; want no change", write, changed, err)
		}
	}
	if changed, err := c.WriteProjectFile("configs/r3/running.conf", []byte("b\n"), 0o600, false); err != nil || !changed {
		t.Errorf("a check of new bytes = %v, %v", changed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "a\n" {
		t.Errorf("a check wrote %q", got)
	}
	if changed, err := c.WriteProjectFile("configs/r3/running.conf", []byte("b\n"), 0o600, true); err != nil || !changed {
		t.Errorf("new bytes = %v, %v", changed, err)
	}
	if got, _ := os.ReadFile(path); string(got) != "b\n" {
		t.Errorf("the file holds %q after new bytes", got)
	}
	if runtime.GOOS != "windows" {
		if changed, err := c.WriteProjectFile("configs/r3/running.conf", []byte("b\n"), 0o640, true); err != nil || !changed {
			t.Errorf("a new mode = %v, %v", changed, err)
		}
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
			t.Errorf("mode %v after a new mode, want 0640", info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %v, %v; want the one file and no temporary one", entries, err)
	}
}

// TestWriteProjectFile_RefusesWhatIsNotTheProjectsToWrite: every path
// ReadProjectFile refuses is refused here too, and so is a component that
// is not a directory, a target that is a directory, data over the bound, a
// mode beyond permission bits, and a run with no project here. Nothing is
// written by any of them.
func TestWriteProjectFile_RefusesWhatIsNotTheProjectsToWrite(t *testing.T) {
	dir := projectWith(t, map[string]string{".pleiades/credentials.yaml": "secret-sentinel", "files/ok": "ok"})
	c := &runbookContext{projectDir: dir}
	for name, want := range map[string]string{
		"":                           "names no file",
		"/etc/passwd":                "is not a path relative",
		"../outside":                 "leaves the project directory",
		".":                          "leaves the project directory",
		".pleiades/credentials.yaml": "holds its credentials",
		".PLEIADES/new":              "holds its credentials",
		"configs/.pleiades/x":        "holds its credentials",
		"files/ok/below":             "is not a directory",
		"files":                      "is not a regular file",
	} {
		for _, write := range []bool{false, true} {
			if _, err := c.WriteProjectFile(name, []byte("x"), 0o600, write); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("WriteProjectFile(%q, write %v) = %v; want it refused saying %q", name, write, err, want)
			}
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml")); string(got) != "secret-sentinel" {
		t.Errorf("the credential file now holds %q", got)
	}
	if _, err := c.WriteProjectFile("big", make([]byte, sdk.MaxProjectFileWrite+1), 0o600, true); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Errorf("data over the bound: %v", err)
	}
	if _, err := c.WriteProjectFile("setuid", []byte("x"), 0o4755, true); err == nil || !strings.Contains(err.Error(), "more than permission bits") {
		t.Errorf("a setuid mode: %v", err)
	}
	if _, err := (&runbookContext{}).WriteProjectFile("files/ok", []byte("x"), 0o600, true); !errors.Is(err, sdk.ErrNoProjectFiles) {
		t.Errorf("a run with no project = %v, want ErrNoProjectFiles", err)
	}
	if _, err := (&runbookContext{projectDir: filepath.Join(dir, "gone")}).WriteProjectFile("x", []byte("x"), 0o600, true); err == nil || !strings.Contains(err.Error(), "opening the project directory") {
		t.Errorf("a project that is not there: %v", err)
	}
}

// TestWriteProjectFile_RefusesASymbolicLink: a link as the target or on
// the way to it is refused, so a write can never land in .pleiades or
// outside the project through one, whatever the link's own name says.
func TestWriteProjectFile_RefusesASymbolicLink(t *testing.T) {
	dir := projectWith(t, map[string]string{".pleiades/credentials.yaml": "secret-sentinel", "files/ok": "ok"})
	if err := os.Symlink(filepath.Join("..", ".pleiades", "credentials.yaml"), filepath.Join(dir, "files", "creds")); err != nil {
		t.Skipf("this system cannot make a symbolic link here: %v", err)
	}
	if err := os.Symlink(".pleiades", filepath.Join(dir, "hidden")); err != nil {
		t.Fatal(err)
	}
	c := &runbookContext{projectDir: dir}
	for _, name := range []string{"files/creds", "hidden/credentials.yaml", "hidden/new"} {
		if _, err := c.WriteProjectFile(name, []byte("x"), 0o600, true); err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("WriteProjectFile(%q) = %v; want it refused naming the link", name, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ".pleiades", "credentials.yaml")); string(got) != "secret-sentinel" {
		t.Errorf("the credential file now holds %q", got)
	}
}

// TestWriteProjectFile_ASystemRefusalIsAnError: a directory the account
// cannot write into, one it cannot search, and a file it cannot read back
// are each an error naming the path, and the file already there is left as
// it was.
func TestWriteProjectFile_ASystemRefusalIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("Unix modes, as an account they bind")
	}
	dir := projectWith(t, map[string]string{"locked/keep.conf": "old", "closed/x": "x", "hidden.conf": "abc"})
	c := &runbookContext{projectDir: dir}
	for path, mode := range map[string]os.FileMode{"locked": 0o500, "closed": 0o600, "hidden.conf": 0o000} {
		p := filepath.Join(dir, path)
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o700) })
	}
	if _, err := c.WriteProjectFile("locked/keep.conf", []byte("new"), 0o600, true); err == nil || !strings.Contains(err.Error(), "locked/keep.conf") {
		t.Errorf("a directory it cannot write: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "locked", "keep.conf")); string(got) != "old" {
		t.Errorf("the file now holds %q", got)
	}
	if _, err := c.WriteProjectFile("closed/x", []byte("y"), 0o600, false); err == nil || !strings.Contains(err.Error(), "closed/x") {
		t.Errorf("a directory it cannot search: %v", err)
	}
	if _, err := c.WriteProjectFile("hidden.conf", []byte("abc"), 0o000, false); err == nil || !strings.Contains(err.Error(), "hidden.conf") {
		t.Errorf("a file it cannot read back: %v", err)
	}
}
