//go:build integration

// Tests for finding main in a clone the way CI checks one out.
package e2e

import (
	"os/exec"
	"strings"
	"testing"
)

// TestMainBranch_FindsMainAsTheCloneHasIt proves the previous release can be
// found in a checkout with no local main, which is what a pull request's CI
// run makes: the merge commit, detached, with main only as origin/main. The
// first CI run failed both upgrade gates on `git merge-base HEAD main`.
func TestMainBranch_FindsMainAsTheCloneHasIt(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "one")

	if got, err := mainBranch(repo); err != nil || got != "main" {
		t.Fatalf("with a local main: %q, %v", got, err)
	}

	git("update-ref", "refs/remotes/origin/main", "HEAD")
	git("checkout", "-q", "--detach")
	git("branch", "-q", "-D", "main")
	if got, err := mainBranch(repo); err != nil || got != "origin/main" {
		t.Fatalf("with only origin/main, detached: %q, %v", got, err)
	}

	git("update-ref", "-d", "refs/remotes/origin/main")
	if _, err := mainBranch(repo); err == nil || !strings.Contains(err.Error(), "fetch-depth: 0") {
		t.Fatalf("with neither: err = %v, want it to say how to fetch main", err)
	}
}
