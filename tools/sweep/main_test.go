// Package main's tests for the sweep tool.
//
// Every test here drives a real git repository created in a temporary
// directory, because the tool's entire safety argument rests on what
// `git check-ignore` answers. A stub that always reported "ignored" would
// pass these tests while the real tool deleted tracked source, which is
// the exact failure RULE 0 exists to prevent.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// newRepo builds a real git repository in a temporary directory, with a
// real .gitignore, and returns its path. The tests use a real repository
// rather than a stub because the whole safety argument of this tool rests
// on `git check-ignore` giving the real answer: a fake that always said
// "ignored" would test nothing that matters.
func newRepo(t *testing.T, gitignore string) string {
	t.Helper()
	dir := t.TempDir()

	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o600); err != nil {
		t.Fatalf("writing .gitignore: %v", err)
	}
	return dir
}

// writeAged writes a file and backdates it, so the age filter has
// something real to act on rather than a mocked clock.
func writeAged(t *testing.T, dir, name string, mode os.FileMode, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), mode); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatalf("backdating %s: %v", name, err)
	}
	return p
}

// TestCollectTakesOnlyIgnoredBuildOutput is the central safety test: a
// tracked binary and an ignored-but-fresh binary both have to survive, and
// only the ignored stale one may be collected.
func TestCollectTakesOnlyIgnoredBuildOutput(t *testing.T) {
	dir := newRepo(t, "pleiades\ncontroller\n*.log\n")

	writeAged(t, dir, "pleiades", 0o755, 30*24*time.Hour)        // ignored, stale: sweep it
	writeAged(t, dir, "controller", 0o755, 1*time.Hour)          // ignored, fresh: keep
	writeAged(t, dir, "Makefile", 0o644, 30*24*time.Hour)        // tracked by pattern absence: keep
	writeAged(t, dir, "run.sh", 0o755, 30*24*time.Hour)          // executable but not ignored: keep
	writeAged(t, dir, "test_output.log", 0o644, 30*24*time.Hour) // ignored suffix, stale: sweep

	found, err := collect(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	got := map[string]bool{}
	for _, c := range found {
		got[filepath.Base(c.path)] = true
	}

	if !got["pleiades"] {
		t.Error("stale ignored binary was not collected, so the sweep would free nothing")
	}
	if !got["test_output.log"] {
		t.Error("stale ignored log was not collected")
	}
	if got["controller"] {
		t.Error("a binary built an hour ago was collected, and it is probably the one in use")
	}
	if got["Makefile"] {
		t.Error("a tracked file was collected, which would delete somebody's work")
	}
	if got["run.sh"] {
		t.Error("an executable git does not ignore was collected, which would delete somebody's work")
	}
}

// TestDirectoriesAreNeverSwept covers the case that protects the internal
// document trees. .IGNORE/ matches .gitignore's `.[A-Z]*` pattern exactly
// as a stale binary matches its own pattern, and it holds the only copy of
// documents nothing can regenerate.
func TestDirectoriesAreNeverSwept(t *testing.T) {
	dir := newRepo(t, ".[A-Z]*\n")

	sub := filepath.Join(dir, ".IGNORE")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "notes.md"), []byte("irreplaceable"), 0o600); err != nil {
		t.Fatalf("writing notes: %v", err)
	}
	when := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(sub, when, when); err != nil {
		t.Fatalf("backdating dir: %v", err)
	}

	found, err := collect(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for _, c := range found {
		if filepath.Base(c.path) == ".IGNORE" {
			t.Fatal("a gitignored directory was collected; the internal document trees would be destroyed")
		}
	}
}

// TestProtectedNamesSurvive proves an ignored file that holds a secret is
// never swept, however old it is. These are ignored because they must not
// be committed, not because they are disposable.
func TestProtectedNamesSurvive(t *testing.T) {
	dir := newRepo(t, "*.log\n.env\nmaster.key\n")
	writeAged(t, dir, ".env", 0o600, 400*24*time.Hour)
	writeAged(t, dir, "master.key", 0o600, 400*24*time.Hour)

	found, err := collect(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("a protected secret file was collected: %v", found)
	}
}

// TestGitIgnoredRejectsTrackedFiles pins the single call the safety rule
// rests on, so a refactor that inverted its meaning fails here.
func TestGitIgnoredRejectsTrackedFiles(t *testing.T) {
	dir := newRepo(t, "build\n")
	writeAged(t, dir, "build", 0o755, time.Hour)
	writeAged(t, dir, "main.go", 0o644, time.Hour)

	if !gitIgnored(dir, filepath.Join(dir, "build")) {
		t.Error("gitIgnored said an ignored file was not ignored, so the sweep would free nothing")
	}
	if gitIgnored(dir, filepath.Join(dir, "main.go")) {
		t.Error("gitIgnored said a normal source file was ignored, which is how a sweep deletes source")
	}
}

// TestGitIgnoredSeparatesOptionsFromPaths pins the "--" in the
// check-ignore call. Callers pass absolute paths today, so this covers the
// defensive half of that argument rather than a live one: a bare name that
// looks like a flag still has to be answered as a path.
func TestGitIgnoredSeparatesOptionsFromPaths(t *testing.T) {
	dir := newRepo(t, "-q\n")
	writeAged(t, dir, "-q", 0o755, time.Hour)

	if !gitIgnored(dir, "-q") {
		t.Error("gitIgnored could not answer for a file named like a flag, so the name was parsed as an option")
	}
}

// TestHumanBytes checks the report's number formatting at each unit
// boundary, since a wrong unit here is how a cleanup tool talks somebody
// into deleting the wrong thing.
func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{95 * 1024 * 1024, "95.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
