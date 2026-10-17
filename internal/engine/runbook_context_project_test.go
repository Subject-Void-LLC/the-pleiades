// Tests that a method reads the project's files and nothing else.
package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// projectWith makes a project directory holding files (name to content,
// slash-separated), and the .pleiades directory a real project has.
func projectWith(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestReadProjectFile_ReadsThePathItNames: a relative path inside the
// project is read, cleaned first, and the bytes are exactly the file's.
func TestReadProjectFile_ReadsThePathItNames(t *testing.T) {
	dir := projectWith(t, map[string]string{"files/app.conf": "a=1\n", "top.bin": "\x00\x01\xff"})
	c := &runbookContext{projectDir: dir}
	for name, want := range map[string]string{"files/app.conf": "a=1\n", "files/../top.bin": "\x00\x01\xff", "./top.bin": "\x00\x01\xff"} {
		got, err := c.ReadProjectFile(name, 1024)
		if err != nil || string(got) != want {
			t.Errorf("ReadProjectFile(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
}

// TestReadProjectFile_RefusesWhatIsNotTheProjectsToGive: a path that is
// absolute, climbs out, reaches .pleiades (in any letter case), is not a
// regular file or is over the bound is refused, and so is any read where
// the run has no project here.
func TestReadProjectFile_RefusesWhatIsNotTheProjectsToGive(t *testing.T) {
	dir := projectWith(t, map[string]string{
		".pleiades/credentials.yaml": "secret-sentinel",
		"files/big":                  strings.Repeat("x", 2048),
		"files/ok":                   "ok",
	})
	c := &runbookContext{projectDir: dir}
	for name, want := range map[string]string{
		"":                           "names no file",
		"/etc/passwd":                "is not a path relative",
		`files\ok`:                   "is not a path relative",
		"C:/x":                       "is not a path relative",
		"..":                         "leaves the project directory",
		"../outside":                 "leaves the project directory",
		"files/../../outside":        "leaves the project directory",
		".":                          "leaves the project directory",
		".pleiades/credentials.yaml": "holds its credentials",
		".PLEIADES/credentials.yaml": "holds its credentials",
		"files/../.pleiades/x":       "holds its credentials",
		"files":                      "is not a regular file",
		"files/big":                  "over the 1024 one read takes",
		"files/absent":               "no such file",
	} {
		got, err := c.ReadProjectFile(name, 1024)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ReadProjectFile(%q) = %q, %v; want it refused saying %q", name, got, err, want)
		}
		if strings.Contains(string(got), "secret-sentinel") || (err != nil && strings.Contains(err.Error(), "secret-sentinel")) {
			t.Errorf("ReadProjectFile(%q) gave the credential file's content", name)
		}
	}
	if _, err := (&runbookContext{}).ReadProjectFile("files/ok", 1024); !errors.Is(err, sdk.ErrNoProjectFiles) {
		t.Errorf("a run with no project = %v, want ErrNoProjectFiles", err)
	}
}

// TestReadProjectFile_RefusesASymbolicLink: a link that stays inside the
// project can still point into .pleiades, which its own name does not
// show, so any link on the path is refused, a directory one included.
func TestReadProjectFile_RefusesASymbolicLink(t *testing.T) {
	dir := projectWith(t, map[string]string{".pleiades/credentials.yaml": "secret-sentinel", "files/ok": "ok"})
	if err := os.Symlink(filepath.Join("..", ".pleiades", "credentials.yaml"), filepath.Join(dir, "files", "creds")); err != nil {
		t.Skipf("this system cannot make a symbolic link here: %v", err)
	}
	if err := os.Symlink(".pleiades", filepath.Join(dir, "hidden")); err != nil {
		t.Fatal(err)
	}
	c := &runbookContext{projectDir: dir}
	for _, name := range []string{"files/creds", "hidden/credentials.yaml"} {
		got, err := c.ReadProjectFile(name, 1024)
		if err == nil || !strings.Contains(err.Error(), "symbolic link") || strings.Contains(string(got), "secret-sentinel") {
			t.Errorf("ReadProjectFile(%q) = %q, %v; want it refused naming the link", name, got, err)
		}
	}
}
