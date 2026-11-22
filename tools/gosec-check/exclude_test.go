// This file holds gosec-check's exclusions to gosec's own matching: compiled
// the way gosec compiles an -exclude-dir value, they leave out .claude and the
// gitignored `.[A-Z]*` directories, and no path that only contains "claude",
// such as an agent's /tmp/claude-<uid> copy.
package main

import (
	"regexp"
	"testing"
)

// gosecExcludeRegexp compiles an -exclude-dir value exactly as gosec v2.30.0's
// ExcludedDirsRegExp does (gosecDirRegexp), failing the test on a bad one.
func gosecExcludeRegexp(t *testing.T, value string) *regexp.Regexp {
	t.Helper()
	re, err := gosecDirRegexp(value)
	if err != nil {
		t.Fatal(err)
	}
	return re
}

// excluded reports whether any of gosec-check's exclusions matches path.
func excluded(t *testing.T, path string) bool {
	t.Helper()
	for _, value := range excludeDirs {
		if gosecExcludeRegexp(t, value).MatchString(path) {
			return true
		}
	}
	return false
}

// TestExclusions_LeaveOutOnlyTheIgnoredDirectories: the module's own files are
// scanned from a plain checkout and from a copy under /tmp/claude-1000, and
// .claude, .IGNORE (with the review copies under .IGNORE/claude-api) and
// .SPECIFICATION are left out. The old bare ".claude" pattern excluded every
// file of the copy.
func TestExclusions_LeaveOutOnlyTheIgnoredDirectories(t *testing.T) {
	for _, scanned := range []string{
		"/home/dev/repo/internal/ent/runtime.go",
		"/tmp/claude-1000/scratch/copy/internal/ent/runtime.go",
		"/srv/myclaude/repo/cmd/runner/main.go",
		"/home/dev/repo/internal/api/x.Test.go",
		"internal/ent/runtime.go",
		"tools/myclaude/x.go",
	} {
		if excluded(t, scanned) {
			t.Errorf("%s is excluded, want it scanned", scanned)
		}
	}
	for _, left := range []string{
		"/home/dev/repo/.claude/worktrees/other/x.go",
		"/home/dev/repo/.IGNORE/claude-api/burn11/src/x.go",
		"/home/dev/repo/.SPECIFICATION/tests/x.go",
		".claude/worktrees/tag-only/internal/sealed/passphrase.go",
		".IGNORE/claude-api/burn5/x.go",
	} {
		if !excluded(t, left) {
			t.Errorf("%s is scanned, want it left out", left)
		}
	}
	if old := gosecExcludeRegexp(t, ".claude"); !old.MatchString("/tmp/claude-1000/scratch/copy/internal/ent/runtime.go") {
		t.Error("the old pattern no longer shows the defect this test exists for")
	}
}
