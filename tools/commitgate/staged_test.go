// Package main: tests for reading what a commit is about to record.
//
// The diff parsing is tested against literal diff text, and the git
// plumbing is tested against a real repository this test creates. The
// second half is the one that counts: the whole point of these functions
// is that they agree with git, and a fake that returned canned output
// would only prove the canned output was canned.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// newRepo creates a real git repository in a temporary directory, makes
// it the process's working directory for the duration of the test, and
// returns its path.
//
// The chdir is what makes this representative: every function under test
// shells out to git with no directory argument, exactly as it does when
// git runs it as a hook from the repository root.
func newRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("reading the working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("entering the temporary repository: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("leaving the temporary repository: %v", err)
		}
	})

	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		// core.hooksPath is pointed at a directory that does not exist so
		// this repository's own hooks never run inside the fixture.
		{"config", "core.hooksPath", filepath.Join(dir, "no-hooks")},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

// write creates a file inside the current repository and stages it.
func write(t *testing.T, path, content string) {
	t.Helper()
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if out, err := exec.Command("git", "add", "--", path).CombinedOutput(); err != nil {
		t.Fatalf("git add %s: %v: %s", path, err, out)
	}
}

// commit records whatever is staged.
func commit(t *testing.T, subject string) {
	t.Helper()
	out, err := exec.Command("git", "commit", "--quiet", "--no-verify", "-m", subject).CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
}

func TestStagedFilesSeesTheFirstCommitWithNoHead(t *testing.T) {
	// A repository whose first commit this is has no HEAD to diff
	// against. Getting this wrong makes the gate fail on exactly the
	// commit that creates a project.
	newRepo(t)
	write(t, "a.go", "package a\n")

	files, err := stagedFiles()
	if err != nil {
		t.Fatalf("stagedFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "a.go" {
		t.Fatalf("stagedFiles = %+v, want one entry for a.go", files)
	}
	if files[0].Kind != changeAdded {
		t.Errorf("Kind = %v, want changeAdded", files[0].Kind)
	}
}

func TestStagedFilesDistinguishesAddedFromModified(t *testing.T) {
	newRepo(t)
	write(t, "old.go", "package old\n")
	commit(t, "chore: seed")

	write(t, "old.go", "package old\n\n// F does a thing.\nfunc F() {}\n")
	write(t, "new.go", "package new\n")

	files, err := stagedFiles()
	if err != nil {
		t.Fatalf("stagedFiles: %v", err)
	}
	kinds := make(map[string]changeKind, len(files))
	for _, f := range files {
		kinds[f.Path] = f.Kind
	}
	if kinds["new.go"] != changeAdded {
		t.Errorf("new.go reported as %v, want changeAdded", kinds["new.go"])
	}
	if kinds["old.go"] != changeModified {
		t.Errorf("old.go reported as %v, want changeModified", kinds["old.go"])
	}
}

func TestStagedFilesExcludesADeletion(t *testing.T) {
	// There is no content left to check, and every rule here is about
	// content.
	newRepo(t)
	write(t, "gone.go", "package gone\n")
	commit(t, "chore: seed")

	if out, err := exec.Command("git", "rm", "--quiet", "gone.go").CombinedOutput(); err != nil {
		t.Fatalf("git rm: %v: %s", err, out)
	}
	files, err := stagedFiles()
	if err != nil {
		t.Fatalf("stagedFiles: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("stagedFiles = %+v, want nothing for a pure deletion", files)
	}
}

func TestStagedContentReadsTheIndexNotTheWorkingTree(t *testing.T) {
	// This is the whole reason the tool reads the index. A file edited
	// after git add has two different contents, and only one of them is
	// about to be committed.
	newRepo(t)
	write(t, "a.go", "package a // staged\n")
	if err := os.WriteFile("a.go", []byte("package a // working tree\n"), 0o600); err != nil {
		t.Fatalf("rewriting a.go: %v", err)
	}

	content, err := stagedContent("a.go")
	if err != nil {
		t.Fatalf("stagedContent: %v", err)
	}
	if got := string(content); got != "package a // staged\n" {
		t.Errorf("stagedContent = %q, want the staged version", got)
	}
}

func TestAddedLinesReportsOnlyWhatThisCommitAdds(t *testing.T) {
	// An untouched line that breaks a rule is not this commit's to answer
	// for, and a gate that said otherwise would fire forever on every
	// commit that touched the file.
	newRepo(t)
	write(t, "notes.md", "one\ntwo\nthree\n")
	commit(t, "docs: seed")

	write(t, "notes.md", "one\ntwo\nINSERTED\nthree\n")
	lines, err := addedLines("notes.md")
	if err != nil {
		t.Fatalf("addedLines: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("addedLines = %+v, want exactly the inserted line", lines)
	}
	if lines[0].Text != "INSERTED" {
		t.Errorf("Text = %q, want %q", lines[0].Text, "INSERTED")
	}
	if lines[0].Number != 3 {
		t.Errorf("Number = %d, want 3: the number must point into the new file", lines[0].Number)
	}
}

func TestParseAddedLinesTracksLineNumbersAcrossHunks(t *testing.T) {
	diff := "diff --git a/x b/x\n" +
		"index 111..222 100644\n" +
		"--- a/x\n" +
		"+++ b/x\n" +
		"@@ -1,0 +2,2 @@\n" +
		"+second\n" +
		"+third\n" +
		"@@ -9,0 +40 @@\n" +
		"+fortieth\n"
	got := parseAddedLines(diff)
	want := []addedLine{{2, "second"}, {3, "third"}, {40, "fortieth"}}
	if len(got) != len(want) {
		t.Fatalf("parseAddedLines returned %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseAddedLinesIgnoresTheFileHeader(t *testing.T) {
	// "+++ b/x" starts with a plus and is not content. Counting it would
	// put a phantom finding at the top of every changed file.
	diff := "--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+real\n"
	got := parseAddedLines(diff)
	if len(got) != 1 || got[0].Text != "real" {
		t.Errorf("parseAddedLines = %+v, want just the real added line", got)
	}
}

func TestParseAddedLinesIsEmptyForADiffWithNoHunks(t *testing.T) {
	if got := parseAddedLines(""); len(got) != 0 {
		t.Errorf("parseAddedLines(\"\") = %+v, want nothing", got)
	}
}

func TestParseHunkStart(t *testing.T) {
	cases := map[string]int{
		"@@ -1,0 +2,2 @@":          2,
		"@@ -9,0 +40 @@":           40,
		"@@ -1 +1 @@ func F() {}":  1,
		"@@ malformed":             0,
		"not a hunk header at all": 0,
		"@@ -1,0 +notanumber,2 @@": 0,
	}
	for header, want := range cases {
		t.Run(header, func(t *testing.T) {
			if got := parseHunkStart(header); got != want {
				t.Errorf("parseHunkStart(%q) = %d, want %d", header, got, want)
			}
		})
	}
}

// gitCombined runs a git command and returns its combined output, for
// the fixture steps that want to report git's own message on failure.
func gitCombined(args ...string) ([]byte, error) {
	return exec.Command("git", args...).CombinedOutput()
}
