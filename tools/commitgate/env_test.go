// This file scrubs git's own environment before any test in this package
// runs.
//
// git exports GIT_DIR into every hook it invokes, and in a linked worktree
// it has to: .git there is a file pointing elsewhere, not a directory git
// can discover. `make push-gate` runs from the pre-push hook, and its
// first stage runs this package's tests, so the test binary inherits a
// GIT_DIR naming the real repository. Every fixture here does `git init`
// in a temporary directory and then `git add`. With GIT_DIR inherited,
// those commands ignore the temporary directory and land on the real
// index, so the files one test stages are still staged when the next test
// looks, and TestStagedFilesSeesTheFirstCommitWithNoHead finds six files
// where it wrote one.
//
// Observed 2026-09-14 pushing from a linked worktree. The main checkout
// never needs GIT_DIR exported, which is why this passed for as long as it
// did and why a hook-driven run from a worktree is what exposed it.
//
// Scrubbed at process level rather than on each exec.Command, because the
// tests also call run() in-process and run() shells out to git itself;
// only the process environment reaches both.
package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain drops every GIT_* variable so each fixture's git commands see
// only the repository they are run inside.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "GIT_") {
			_ = os.Unsetenv(name)
		}
	}
	os.Exit(m.Run())
}
