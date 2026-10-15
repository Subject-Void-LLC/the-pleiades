// Package gooverlay builds and tests Go source a test generated, without
// writing it into the module: the files live in a temporary tree laid out
// like the module's, and the go command reads them through -overlay as if
// they stood at their module paths.
//
// A test that wrote generated packages into the module tree itself raced
// every other test reading that tree. A package listed in one moment and
// removed the next failed a walk with "no such file or directory"
// (tools/internal/flakegate's container scan did, beside tools/gencatalog's
// dogfood test), and a file added to an existing package was compiled into
// whatever else built that package at the time.
package gooverlay

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tree is generated source standing in the module rooted at Root.
type Tree struct {
	// Root is the module's directory, which is never written.
	Root string
	// Dir is where generated files go, laid out as they would be under
	// Root: a scaffold's --dir, or the parent of each file's module path.
	Dir string

	module string
	work   string
}

// New returns an empty tree for the module at root, in directories the
// test removes when it ends.
func New(t testing.TB, root string) *Tree {
	t.Helper()
	module, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("gooverlay: %v", err)
	}
	return &Tree{Root: root, Dir: t.TempDir(), module: module, work: t.TempDir()}
}

// modulePath reads the module directive from the go.mod at path.
func modulePath(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- the module's own go.mod, from a test's root
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(lines.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	if err := lines.Err(); err != nil {
		return "", err
	}
	return "", &fs.PathError{Op: "read module directive", Path: path, Err: fs.ErrNotExist}
}

// Write writes content at rel, a path relative to the module's root, under
// Dir.
func (tr *Tree) Write(t testing.TB, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(tr.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("gooverlay: %v", err)
	}
	if err := os.WriteFile(full, content, 0o600); err != nil {
		t.Fatalf("gooverlay: %v", err)
	}
}

// Build runs go build on each package import path, with every file under
// Dir read at its module path.
func (tr *Tree) Build(t testing.TB, pkgs ...string) {
	t.Helper()
	tr.goCmd(t, append([]string{"build", "-overlay=" + tr.overlay(t)}, pkgs...)...)
}

// Test compiles pkg's tests with every file under Dir read at its module
// path, runs them with args (test binary flags, such as -test.run), and
// returns what they printed. They run in the package's directory, its
// stand-in under Dir when the module has none.
//
// go vet is not run: it works in the package's own directory, which for a
// generated package exists only in the overlay. Generated code the module
// commits is vetted with the rest of it.
func (tr *Tree) Test(t testing.TB, pkg string, args ...string) string {
	t.Helper()
	rel, ok := strings.CutPrefix(pkg, tr.module+"/")
	if !ok {
		t.Fatalf("gooverlay: %s is not a package of module %s", pkg, tr.module)
	}
	bin := filepath.Join(tr.work, strings.ReplaceAll(rel, "/", "_")+".test")
	tr.goCmd(t, "test", "-c", "-vet=off", "-overlay="+tr.overlay(t), "-o", bin, pkg)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("gooverlay: go test -c %s built no test binary, so no test of it ran: %v", pkg, err)
	}
	dir := filepath.Join(tr.Root, filepath.FromSlash(rel))
	if _, err := os.Stat(dir); err != nil {
		dir = filepath.Join(tr.Dir, filepath.FromSlash(rel))
	}
	cmd := exec.Command(bin, append([]string{"-test.v"}, args...)...) // #nosec G204 -- a test binary this call just built from a test's own generated source, with test flags that test passed; internal/archtest keeps this package out of every production build
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gooverlay: the tests of %s failed: %v\n%s", pkg, err, out)
	}
	return string(out)
}

// overlay writes the go command's overlay file for the files under Dir now
// and returns its path.
func (tr *Tree) overlay(t testing.TB) string {
	t.Helper()
	replace := map[string]string{}
	err := filepath.WalkDir(tr.Dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(tr.Dir, path)
		if err != nil {
			return err
		}
		replace[filepath.Join(tr.Root, rel)] = path
		return nil
	})
	if err != nil {
		t.Fatalf("gooverlay: %v", err)
	}
	if len(replace) == 0 {
		t.Fatalf("gooverlay: nothing was generated under %s", tr.Dir)
	}
	data, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatalf("gooverlay: %v", err)
	}
	path := filepath.Join(tr.work, "overlay.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("gooverlay: %v", err)
	}
	return path
}

// goCmd runs the go command in the module's root.
func (tr *Tree) goCmd(t testing.TB, args ...string) {
	t.Helper()
	cmd := exec.Command("go", args...) // #nosec G204 -- the go command by its literal name, with subcommands fixed here and package paths and an overlay file a test supplied; internal/archtest keeps this package out of every production build
	cmd.Dir = tr.Root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gooverlay: go %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
