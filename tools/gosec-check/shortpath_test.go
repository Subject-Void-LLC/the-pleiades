// This file tests the short path's link: one per checkout, the same on every
// run, made only in a private directory, never taken over from another
// checkout, and removed once its checkout is gone.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// requireSymlinks skips a test on a machine that cannot make a symlink
// (Windows without the privilege).
func requireSymlinks(t *testing.T) {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "probe")
	err := os.Symlink(t.TempDir(), probe)
	need.Require(t, need.Symlinks, err == nil, "this machine cannot make a symbolic link here")
}

// TestStableShortRoot_OneLinkPerCheckoutEveryRun: two runs from one checkout
// get the same link, which leads to it; another checkout gets another link;
// and the link's name is shortLinkHex hex digits, so its path stays short.
func TestStableShortRoot_OneLinkPerCheckoutEveryRun(t *testing.T) {
	requireSymlinks(t)
	base := filepath.Join(t.TempDir(), shortLinkDir)
	root, other := t.TempDir(), t.TempDir()

	first, err := stableShortRoot(base, root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := stableShortRoot(base, root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("two runs made %s and %s, want one link every run", first, second)
	}
	if target, err := os.Readlink(first); err != nil || target != root {
		t.Errorf("%s leads to %q (%v), want %s", first, target, err, root)
	}
	if !isLinkName(filepath.Base(first)) {
		t.Errorf("link name %q is not %d hex digits", filepath.Base(first), shortLinkHex)
	}
	otherLink, err := stableShortRoot(base, other)
	if err != nil {
		t.Fatal(err)
	}
	if otherLink == first {
		t.Errorf("two checkouts share the link %s", first)
	}
	info, err := os.Lstat(base)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("base %s is %v, want a directory only its owner reads", base, info.Mode())
	}
}

// TestStableShortRoot_RefusesALinkToAnotherCheckout: a link under this
// checkout's name that leads elsewhere is reported and left as it is.
func TestStableShortRoot_RefusesALinkToAnotherCheckout(t *testing.T) {
	requireSymlinks(t)
	base := filepath.Join(t.TempDir(), shortLinkDir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	root, elsewhere := t.TempDir(), t.TempDir()
	link := shortLinkPath(base, root)
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	if _, err := stableShortRoot(base, root); err == nil || !strings.Contains(err.Error(), "not to this checkout") {
		t.Fatalf("err = %v, want a refusal naming the other checkout", err)
	}
	if target, _ := os.Readlink(link); target != elsewhere {
		t.Errorf("the other checkout's link now leads to %q, want it left at %s", target, elsewhere)
	}
}

// TestStableShortRoot_RefusesABaseOthersCanWrite: a base directory that is a
// symlink, or that a group or other users may write in, is refused before any
// link is made in it.
func TestStableShortRoot_RefusesABaseOthersCanWrite(t *testing.T) {
	requireSymlinks(t)
	root := t.TempDir()

	target := t.TempDir()
	viaLink := filepath.Join(t.TempDir(), "base")
	if err := os.Symlink(target, viaLink); err != nil {
		t.Fatal(err)
	}
	if _, err := stableShortRoot(viaLink, root); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a base that is a symlink: err = %v, want it refused", err)
	}

	if runtime.GOOS == "windows" {
		return
	}
	open := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o777); err != nil { // #nosec G302 -- the world-writable directory this test exists to refuse
		t.Fatal(err)
	}
	if _, err := stableShortRoot(open, root); err == nil || !strings.Contains(err.Error(), "other users") {
		t.Errorf("a world-writable base: err = %v, want it refused", err)
	}
	if entries, _ := os.ReadDir(open); len(entries) != 0 {
		t.Errorf("a link was made in the refused base: %v", entries)
	}
}

// TestStableShortRoot_PrunesLinksWhoseCheckoutIsGone: once a checkout is
// removed, the next run removes its link, and leaves links that still lead
// somewhere and anything not named as a link.
func TestStableShortRoot_PrunesLinksWhoseCheckoutIsGone(t *testing.T) {
	requireSymlinks(t)
	base := filepath.Join(t.TempDir(), shortLinkDir)
	gone := filepath.Join(t.TempDir(), "removed-checkout")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	goneLink, err := stableShortRoot(base, gone)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := stableShortRoot(base, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stranger := filepath.Join(base, "not-a-link-name")
	if err := os.Symlink(gone, stranger); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	if _, err := stableShortRoot(base, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(goneLink); !os.IsNotExist(err) {
		t.Errorf("the removed checkout's link %s is still there (%v)", goneLink, err)
	}
	for _, left := range []string{kept, stranger} {
		if _, err := os.Lstat(left); err != nil {
			t.Errorf("%s was removed: %v", left, err)
		}
	}
}
