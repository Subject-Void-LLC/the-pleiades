// Tests for prscope against a real git repository holding a real module:
// which changes name which packages, and what names none.
package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scratchModule is a module in a fresh git repository with one commit on
// main and a branch checked out from it: package a with a testdata
// fixture, package b embedding a subdirectory with no Go files, package c
// that the branch leaves alone, and a docs directory.
func scratchModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":               "module example.com/scope\n\ngo 1.22\n",
		"a/a.go":               "// Package a is a fixture.\npackage a\n",
		"a/testdata/input.txt": "one\n",
		"b/b.go":               "// Package b is a fixture.\npackage b\n",
		"b/types/kind.json":    "{}\n",
		"c/c.go":               "// Package c is a fixture.\npackage c\n",
		"docs/guide.md":        "# guide\n",
	}
	for name, body := range files {
		write(t, dir, name, body)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.name=scope", "-c", "user.email=scope@example.com", "commit", "-q", "-m", "base"},
		{"checkout", "-q", "-b", "topic"},
	} {
		git(t, dir, args...)
	}
	return dir
}

// write creates name under dir holding body.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// git runs git in dir, failing t on an error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// TestChangedPackages_NamesThePackageAboveEachChange changes a testdata
// fixture (committed), an embedded file in a directory with no Go files
// (unstaged), a new package (untracked) and a doc and go.mod (naming
// nothing), and requires exactly a, b and the new package.
func TestChangedPackages_NamesThePackageAboveEachChange(t *testing.T) {
	dir := scratchModule(t)
	write(t, dir, "a/testdata/input.txt", "two\n")
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=scope", "-c", "user.email=scope@example.com", "commit", "-q", "-m", "change a's fixture")
	write(t, dir, "b/types/kind.json", "{\"kind\": 1}\n")
	write(t, dir, "d/d.go", "// Package d is new.\npackage d\n")
	write(t, dir, "docs/guide.md", "# guide, changed\n")
	write(t, dir, "go.mod", "module example.com/scope\n\ngo 1.23\n")

	got, _, _, err := changedPackages(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := "example.com/scope/a example.com/scope/b example.com/scope/d"; strings.Join(got, " ") != want {
		t.Errorf("changed packages = %v, want %s", got, want)
	}
}

// TestChangedPackages_NothingChangedNamesNothing is a branch with no
// change of its own.
func TestChangedPackages_NothingChangedNamesNothing(t *testing.T) {
	got, _, _, err := changedPackages(scratchModule(t), "main")
	if err != nil || len(got) != 0 {
		t.Errorf("an unchanged branch = %v, %v; want nothing", got, err)
	}
}

// TestChangedPackages_AnUnknownBaseIsRefused names a base that does not
// exist, and a directory that is no module.
func TestChangedPackages_AnUnknownBaseIsRefused(t *testing.T) {
	dir := scratchModule(t)
	if _, _, _, err := changedPackages(dir, "no-such-ref"); err == nil || !strings.Contains(err.Error(), "no-such-ref") {
		t.Errorf("an unknown base: %v, want a refusal naming it", err)
	}
	if err := os.Remove(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a/a.go", "// Package a is changed.\npackage a\n")
	if _, _, _, err := changedPackages(dir, "main"); err == nil || !strings.Contains(err.Error(), "go list") {
		t.Errorf("a directory with no module: %v, want go list's refusal", err)
	}
	if _, err := run(dir, "no-such-program-prscope"); err == nil {
		t.Error("running a program that does not exist succeeded")
	}
}

// TestRealMain_PrintsOnePackagePerLineAndExitsByOutcome runs the command
// as the Makefile does: the packages one per line and 0, a base that does
// not exist 1 with the reason, and an unknown flag 2.
func TestRealMain_PrintsOnePackagePerLineAndExitsByOutcome(t *testing.T) {
	dir := scratchModule(t)
	write(t, dir, "a/a.go", "// Package a is changed.\npackage a\n")
	write(t, dir, "c/c.go", "// Package c is changed.\npackage c\n")
	var stdout, stderr bytes.Buffer
	if code := realMain(dir, []string{"-base", "main"}, &stdout, &stderr); code != 0 || stdout.String() != "example.com/scope/a\nexample.com/scope/c\n" {
		t.Errorf("exit %d, printed %q (%s); want a and c, one per line", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	if code := realMain(dir, []string{"-base", "no-such-ref"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "prscope: finding where HEAD left no-such-ref") || stdout.Len() != 0 {
		t.Errorf("an unknown base: exit %d, stderr %q", code, stderr.String())
	}
	if code := realMain(dir, []string{"-bogus"}, &stdout, &stderr); code != 2 {
		t.Errorf("an unknown flag: exit %d, want 2", code)
	}
}

// TestChangedPackages_AddsEachDirectImporter changes a package that one
// package imports in its code, one only in its internal tests and one only
// in its external tests, and one imports only through another: the three
// direct importers run too, named as added, and the indirect one does not
// (FAILURE_PATTERNS 631: pkg/netcli changed, and internal/catalog/wait,
// which imports it, broke only in CI).
func TestChangedPackages_AddsEachDirectImporter(t *testing.T) {
	dir := scratchModule(t)
	for name, body := range map[string]string{
		"user/user.go":          "// Package user imports c.\npackage user\n\nimport _ \"example.com/scope/c\"\n",
		"intest/intest.go":      "// Package intest imports c in its tests.\npackage intest\n",
		"intest/intest_test.go": "package intest\n\nimport _ \"example.com/scope/c\"\n",
		"xtest/xtest.go":        "// Package xtest imports c in its external tests.\npackage xtest\n",
		"xtest/xtest_test.go":   "package xtest_test\n\nimport _ \"example.com/scope/c\"\n",
		"indirect/indirect.go":  "// Package indirect imports c only through user.\npackage indirect\n\nimport _ \"example.com/scope/user\"\n",
	} {
		write(t, dir, name, body)
	}
	git(t, dir, "add", ".")
	git(t, dir, "-c", "user.name=scope", "-c", "user.email=scope@example.com", "commit", "-q", "-m", "importers")
	git(t, dir, "branch", "-f", "main", "HEAD")
	write(t, dir, "c/c.go", "// Package c is changed.\npackage c\n")

	got, importers, _, err := changedPackages(dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := "example.com/scope/c example.com/scope/intest example.com/scope/user example.com/scope/xtest"; strings.Join(got, " ") != want {
		t.Errorf("changed packages = %v, want %s", got, want)
	}
	if want := "example.com/scope/intest example.com/scope/user example.com/scope/xtest"; strings.Join(importers, " ") != want {
		t.Errorf("importers added = %v, want %s", importers, want)
	}
	var stdout, stderr bytes.Buffer
	if code := realMain(dir, []string{"-base", "main"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "3 package(s) import a changed one, so they run too (FAILURE_PATTERNS 631)") {
		t.Errorf("exit %d, stderr %q", code, stderr.String())
	}
}
