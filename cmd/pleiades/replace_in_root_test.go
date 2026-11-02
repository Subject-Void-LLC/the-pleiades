// Package main tests, for replaceInRoot: a symlink at the name or at the
// temporary name is replaced or removed, never written through
// (FAILURE_PATTERNS 792).
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestReplaceInRoot_ReplacesALinkAndKeepsItsTarget holds replaceInRoot to
// FAILURE_PATTERNS 792: a symlink at the name, or at the temporary name, is
// replaced or removed, never written through, and the result is a private
// regular file.
func TestReplaceInRoot_ReplacesALinkAndKeepsItsTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "doc.json")); err != nil {
		t.Fatal(err)
	}
	// A link left at the temporary name this process would use.
	tmp := ".doc.json.pleiades-" + strconv.Itoa(os.Getpid()) + ".tmp"
	if err := os.Symlink(outside, filepath.Join(dir, tmp)); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := replaceInRoot(root, "doc.json", []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("replaceInRoot: %v", err)
	}

	if got, _ := os.ReadFile(outside); string(got) != "untouched\n" {
		t.Errorf("the link's target was written: %q", got)
	}
	info, err := os.Lstat(filepath.Join(dir, "doc.json"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("doc.json = %v %v, want a 0600 regular file", info, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "doc.json")); string(got) != "{}\n" {
		t.Errorf("doc.json holds %q", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, tmp)); err == nil {
		t.Error("the temporary name was left behind")
	}
}
