// This file scrubs git's own environment before any test in this package
// runs.
//
// TestEmbeddedAssetsAreTrackedByGit shells out to `git ls-files` to prove
// every embedded asset is committed. git exports GIT_DIR into every hook it
// invokes, and `make push-gate` runs from the pre-push hook, so this
// package's tests inherit a GIT_DIR naming the repository being pushed.
// From a linked worktree that GIT_DIR points at a gitdir under
// .git/worktrees, and `git ls-files` run against it with the test's cwd
// deep inside the package no longer resolves the asset paths, so
// --error-unmatch reports them as untracked and the test fails on a
// correctly committed tree.
//
// Observed 2026-09-14 pushing from a linked worktree; the same GIT_DIR
// inheritance that tools/commitgate's own TestMain already scrubs for the
// same reason. A normal checkout never exports GIT_DIR, which is why this
// passed for as long as it did.
//
// Unsetting every GIT_* variable makes git discover the repository from the
// test's working directory, which is the plain checkout the assets are
// committed in, in both a linked worktree and the main checkout.
package static_test

import (
	"os"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable so the git commands these tests run
// see the checkout they run inside rather than one a hook named for them.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "GIT_") {
			_ = os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}
