// Package main: tests for the record archive rule, against literal numstat
// text and against a real repository's index.
package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCheckArchives fails an archive left shorter, warns about one edited
// in place, and passes one appended to.
func TestCheckArchives(t *testing.T) {
	findings := checkArchives([]archiveChange{
		{Path: "FAILURE_PATTERNS_ARCHIVE.md", Added: 70, Removed: 12825},
		{Path: "LESSONS_LEARNED_ARCHIVE.md", Added: 3, Removed: 3},
		{Path: "HANDOFF_ARCHIVE.md", Added: 40},
	})
	if len(findings) != 2 || findings[0].Severity != SeverityFail || !strings.Contains(findings[0].Detail, "12755 line(s) shorter") ||
		findings[1].Severity != SeverityWarn || findings[1].File != "LESSONS_LEARNED_ARCHIVE.md" {
		t.Errorf("findings = %+v", findings)
	}
}

// TestParseNumstat reads counts and paths, and refuses a binary count.
func TestParseNumstat(t *testing.T) {
	changes, err := parseNumstat("70\t12825\tFAILURE_PATTERNS_ARCHIVE.md\n0\t5804\tLESSONS_LEARNED_ARCHIVE.md\n")
	if err != nil || len(changes) != 2 || changes[0].Removed != 12825 || changes[1].Path != "LESSONS_LEARNED_ARCHIVE.md" {
		t.Errorf("%+v, %v", changes, err)
	}
	for _, bad := range []string{"-\t-\tx_ARCHIVE.md", "12 x_ARCHIVE.md"} {
		if _, err := parseNumstat(bad); err == nil {
			t.Errorf("%q was read", bad)
		}
	}
	if changes, err := parseNumstat(""); err != nil || len(changes) != 0 {
		t.Errorf("no output: %+v, %v", changes, err)
	}
}

// TestArchiveChanges_SeesATruncationAndADeletion: against a real index, a
// commit that cuts an archive down or deletes it fails, and one that
// appends passes.
func TestArchiveChanges_SeesATruncationAndADeletion(t *testing.T) {
	newRepo(t)
	write(t, "FAILURE_PATTERNS_ARCHIVE.md", "## 1. one\n\nbody\n\n## 2. two\n\nbody\n")
	write(t, "LESSONS_LEARNED_ARCHIVE.md", "## 1. lesson\n")
	if out, err := exec.Command("git", "commit", "--quiet", "-m", "records").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}

	write(t, "FAILURE_PATTERNS_ARCHIVE.md", "## 1. one\n\nbody\n\n## 2. two\n\nbody\n\n## 3. three\n")
	if findings := mustArchiveFindings(t); len(findings) != 0 {
		t.Errorf("an append: %+v", findings)
	}

	write(t, "FAILURE_PATTERNS_ARCHIVE.md", "## 3. three\n")
	if out, err := exec.Command("git", "rm", "--quiet", "LESSONS_LEARNED_ARCHIVE.md").CombinedOutput(); err != nil {
		t.Fatalf("git rm: %v: %s", err, out)
	}
	findings := mustArchiveFindings(t)
	if len(findings) != 2 || findings[0].Severity != SeverityFail || findings[1].Severity != SeverityFail {
		t.Errorf("a truncation and a deletion: %+v", findings)
	}
}

func mustArchiveFindings(t *testing.T) []Finding {
	t.Helper()
	changes, err := archiveChanges()
	if err != nil {
		t.Fatal(err)
	}
	return checkArchives(changes)
}
