//go:build !windows

// This file tests the link directory's ownership check on Linux and macOS: a
// directory another user owns is refused before any link is made in it.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStableShortRoot_RefusesABaseAnotherUserOwns: with the directory's
// owner made to look like someone else, the base is refused and nothing is
// made in it. Without the check, another user who made the directory first
// could swap the link after it was read.
func TestStableShortRoot_RefusesABaseAnotherUserOwns(t *testing.T) {
	requireSymlinks(t)
	base := filepath.Join(t.TempDir(), shortLinkDir)
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	me := os.Getuid()
	currentUID = func() int { return me + 1 }
	t.Cleanup(func() { currentUID = os.Getuid })
	if _, err := stableShortRoot(base, t.TempDir()); err == nil || !strings.Contains(err.Error(), "not to this user") {
		t.Fatalf("a base another user owns: err = %v, want it refused", err)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("a link was made in the refused base: %v", entries)
	}
}
