// Tests for where a check's settings come from and the commit it tests,
// against real git repositories.
package oscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// inRepo runs t from a fresh git repository with one commit, and returns
// its directory with symbolic links resolved, as git reports it (a
// temporary directory on macOS is under a link).
func inRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=m", "-c", "user.email=m@example.com", "commit", "-q", "--allow-empty", "-m", "base"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// writeFile writes content to dir/rel, making its directory.
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestLoad: the environment gives every key when it sets any of them, the
// file gives them when it sets none, and nothing at all is no settings.
func TestLoad(t *testing.T) {
	dir := inRepo(t)
	t.Setenv("K_ONE", "")
	t.Setenv("K_TWO", "")
	if s, err := Load("tests/x/.env", "K_ONE", "K_TWO"); err != nil || s.File != "" || len(s.Values) != 0 {
		t.Fatalf("with neither: %+v, %v", s, err)
	}
	writeFile(t, dir, "tests/x/.env", "# mine\n\nK_ONE='a'\nK_TWO=\"b c\"\n")
	s, err := Load("tests/x/.env", "K_ONE", "K_TWO")
	if err != nil || s.File != filepath.Join(dir, "tests/x/.env") || s.Values["K_ONE"] != "a" || s.Values["K_TWO"] != "b c" {
		t.Fatalf("from the file: %+v, %v", s, err)
	}
	// One key in the environment wins over the whole file, so a run never
	// takes one value from each.
	t.Setenv("K_ONE", "env")
	s, err = Load("tests/x/.env", "K_ONE", "K_TWO")
	if err != nil || s.File != "" || s.Values["K_ONE"] != "env" || s.Values["K_TWO"] != "" {
		t.Fatalf("the environment over the file: %+v, %v", s, err)
	}
}

// TestLoad_AWorktreeReadsTheMainWorktreesFile: a linked worktree with no
// file of its own reads the main worktree's; one of its own wins.
func TestLoad_AWorktreeReadsTheMainWorktreesFile(t *testing.T) {
	dir := inRepo(t)
	t.Setenv("K", "")
	writeFile(t, dir, "tests/x/.env", "K=main\n")
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "worktree", "add", "-q", wt).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	t.Chdir(wt)
	if s, err := Load("tests/x/.env", "K"); err != nil || s.Values["K"] != "main" || s.File != filepath.Join(dir, "tests/x/.env") {
		t.Fatalf("the main worktree's file: %+v, %v", s, err)
	}
	writeFile(t, wt, "tests/x/.env", "K=mine\n")
	if s, err := Load("tests/x/.env", "K"); err != nil || s.Values["K"] != "mine" {
		t.Fatalf("the worktree's own file: %+v, %v", s, err)
	}
}

// TestParseEnv refuses a line that is not KEY=VALUE and a key it was not
// asked for, naming the line, so a misspelled setting is not ignored.
func TestParseEnv(t *testing.T) {
	for content, want := range map[string]string{
		"K_ONE=a\nK_TOW=b\n": "line 2 sets K_TOW; the settings are K_ONE and K_TWO",
		"just words\n":       "line 1 is not KEY=VALUE",
	} {
		if _, err := ParseEnv([]byte(content), "K_ONE", "K_TWO"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", content, err, want)
		}
	}
	if _, err := ParseEnv([]byte("X=1\n"), "K"); err == nil || !strings.Contains(err.Error(), "the only setting is K") {
		t.Errorf("one key: %v", err)
	}
	got, err := ParseEnv([]byte("K='unbalanced\"\n"), "K")
	if err != nil || got["K"] != `'unbalanced"` {
		t.Errorf("mismatched quotes are kept: %q, %v", got["K"], err)
	}
}

// TestPrivateDir is under each worktree's own git directory.
func TestPrivateDir(t *testing.T) {
	dir := inRepo(t)
	if got, err := PrivateDir("p"); err != nil || got != filepath.Join(dir, ".git", "p") {
		t.Fatalf("PrivateDir = %q, %v", got, err)
	}
}

// TestResolveCommit is the full hash and whether the tree has changes, and
// refuses what is not a commit.
func TestResolveCommit(t *testing.T) {
	dir := inRepo(t)
	sha, dirty, err := ResolveCommit("HEAD")
	if err != nil || len(sha) != 40 || dirty {
		t.Fatalf("clean: %q, %v, %v", sha, dirty, err)
	}
	writeFile(t, dir, "new.txt", "x")
	if _, dirty, err := ResolveCommit("HEAD"); err != nil || !dirty {
		t.Fatalf("an untracked file: dirty %v, %v", dirty, err)
	}
	if _, _, err := ResolveCommit("nosuchref"); err == nil || !strings.Contains(err.Error(), `"nosuchref" is not a commit`) {
		t.Fatalf("not a commit: %v", err)
	}
}

// TestSmallHelpers covers ExpandHome, FileExists, FirstOf and Short.
func TestSmallHelpers(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := ExpandHome("~/x/y"); got != filepath.Join(home, "x", "y") {
		t.Errorf("ExpandHome = %q", got)
	}
	if got := ExpandHome("/abs/~/x"); got != "/abs/~/x" {
		t.Errorf("ExpandHome changed a path without a leading ~/: %q", got)
	}
	dir := t.TempDir()
	writeFile(t, dir, "f", "x")
	if !FileExists(filepath.Join(dir, "f")) || FileExists(dir) || FileExists(filepath.Join(dir, "none")) {
		t.Error("FileExists is true only for an existing regular file")
	}
	if FirstOf("", "a", "b") != "a" || FirstOf("", "") != "" {
		t.Error("FirstOf")
	}
	if Short("0123456789abcdef") != "0123456789ab" || Short("abc") != "abc" {
		t.Error("Short")
	}
}
