// This file holds the one answer to "where is the module root", which
// every test that has to reach a file the Go toolchain does not compile
// needs: a Dockerfile, docker-compose.yml, the Helm chart, the runbook
// examples.
//
// It was unexported and private to the Ansible image helper until Phase
// 20's packaging Release Gate needed the same walk from a test three
// directories away. Copying it would have been the third copy in this
// repository (cmd/pleiades/e2e_test.go has its own), and a copy that
// hardcodes "../.." is only correct at one nesting depth, which is
// exactly the bug this walk exists to avoid.
package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// RepoRoot walks up from the test's working directory to the module root,
// rather than hardcoding a "../.." that is only correct for callers at one
// particular depth.
//
// It fails the calling test rather than returning an error, because every
// caller is a test that cannot do anything useful without the answer, and
// because a wrong root produces a confusing failure much later (a docker
// build with the wrong context, a compose file that does not exist) rather
// than at the point of the mistake.
func RepoRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatalf("resolving the working directory: %v", err)
	}
	for {
		// go.mod marks the module root. Walking to it, rather than to the
		// first directory holding a .git, keeps this correct inside a git
		// worktree, where .git is a file rather than a directory.
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("no go.mod found above the test's working directory")
		}
		dir = parent
	}
}
