// This file scrubs git's own environment before any test in this package
// runs.
//
// git exports GIT_DIR into every hook it invokes, and in a linked worktree
// it has to: .git there is a file pointing elsewhere, not a directory git
// can discover. `make push-gate` runs from the pre-push hook and drives
// this package's tests, so the test binary inherits a GIT_DIR naming the
// real repository. Every fixture here does `git init` in a temporary
// directory and then `git config`. With GIT_DIR inherited, those commands
// ignore the temporary directory and land on the real one, writing
// user.name and user.email into the checkout's own config, and every
// check-ignore answer the tests assert on is then answered by the wrong
// repository.
//
// This is the third package to need the scrub. tools/commitgate and
// internal/ui/static were fixed the same way on 2026-09-14 after a
// worktree push wrote core.worktree into the real config and quietly broke
// every git operation run from a subdirectory.
//
// Scrubbed at process level rather than on each exec.Command, because the
// tool's own gitIgnored and repoRoot shell out to git themselves and only
// the process environment reaches those.
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
