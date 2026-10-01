// doctor's checks run for real, on the machine running the tests, since
// what they report is that machine. The assertions are about shape and
// about the answers this repository guarantees wherever it is checked out.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// atRepoRoot runs the test from the repository root, where doctor runs.
func atRepoRoot(t *testing.T) {
	t.Helper()
	restore, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })
}

// TestChecks_AnswerEveryQuestion runs every check and proves each line is
// well formed: a known status, a subject and a detail, with a fix line
// always saying what to do.
func TestChecks_AnswerEveryQuestion(t *testing.T) {
	atRepoRoot(t)
	var lines []line
	lines = append(lines, checkGo()...)
	lines = append(lines, checkDocker())
	lines = append(lines, checkTools()...)
	lines = append(lines, checkHooks())
	lines = append(lines, checkOptional()...)
	if len(lines) < 9 {
		t.Fatalf("only %d lines: %+v", len(lines), lines)
	}
	for _, l := range lines {
		switch l.status {
		case "ok", "fix", "info":
		default:
			t.Errorf("line %+v has an unknown status", l)
		}
		if l.what == "" || l.detail == "" {
			t.Errorf("line %+v is missing its subject or detail", l)
		}
	}
}

// TestCheckGo_TheToolchainRunningThisTestIsEnough proves the Go check
// passes for the toolchain running the tests, which go.mod's own toolchain
// line makes true wherever this module builds.
func TestCheckGo_TheToolchainRunningThisTestIsEnough(t *testing.T) {
	atRepoRoot(t)
	got := checkGo()
	if len(got) != 1 || got[0].status != "ok" {
		t.Fatalf("checkGo = %+v, want ok", got)
	}
}

// TestCheckGo_OutsideTheRepositoryIsAFix proves running doctor from the
// wrong directory says so rather than guessing.
func TestCheckGo_OutsideTheRepositoryIsAFix(t *testing.T) {
	restore, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(restore) })
	if got := checkGo(); len(got) != 1 || got[0].status != "fix" {
		t.Fatalf("checkGo outside the repository = %+v, want fix", got)
	}
	if got := checkTools(); len(got) != 1 || got[0].status != "fix" {
		t.Fatalf("checkTools outside the repository = %+v, want fix", got)
	}
}

// TestOrNone names an unknown version.
func TestOrNone(t *testing.T) {
	if orNone("") != "unknown version" || orNone("v1") != "v1" {
		t.Fatal("orNone")
	}
}
