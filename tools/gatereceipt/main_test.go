// These tests drive the receipt against REAL git repositories created per
// case, rather than against a fake, because every decision this command
// makes is a question it asks git: what is HEAD, is the tree dirty, where
// does .git live. A fake would be asserting the test's own answers.
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newRepo builds a real repository with one commit and chdirs into it.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// A test must not depend on the developer's own git identity, nor
		// on their hooks: core.hooksPath is set repository-wide here, and
		// an inherited one would run the real pre-push inside a fixture.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--quiet")
	run("config", "core.hooksPath", "")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatalf("writing a fixture file: %v", err)
	}
	run("add", "a.txt")
	run("commit", "--quiet", "-m", "first")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	return dir
}

func headOf(t *testing.T) string {
	t.Helper()
	sha, err := revParse("HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return sha
}

// TestWriteThenVerify is the whole contract in one pass: a gate that
// passed lets that commit through, and nothing else.
func TestWriteThenVerify(t *testing.T) {
	newRepo(t)

	if err := runWrite([]string{"--target", "push-gate"}); err != nil {
		t.Fatalf("runWrite on a clean tree: %v", err)
	}
	if err := runVerify([]string{"--commit", headOf(t)}); err != nil {
		t.Fatalf("runVerify for the commit just recorded: %v", err)
	}
}

// TestWriteRefusesADirtyTree is the property that makes this stricter than
// the hook it replaces.
//
// A receipt names a commit. A gate run against a tree with uncommitted
// edits proved something about code that is not in any commit, so issuing
// a receipt from it would make the receipt's own sentence false. The old
// arrangement had exactly this gap and nothing noticed: it ran the suite
// over the working tree and then pushed commits.
func TestWriteRefusesADirtyTree(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatalf("dirtying the tree: %v", err)
	}

	err := runWrite([]string{"--target", "push-gate"})
	if err == nil {
		t.Fatal("runWrite issued a receipt from a dirty tree, so the receipt names a commit nobody tested")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("refusal does not say why: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git", receiptName)); !os.IsNotExist(statErr) {
		t.Error("a receipt file was written despite the refusal")
	}
}

// TestWriteRefusesAnUntrackedFile covers the half of "dirty" a plain diff
// is blind to: a new file the gate compiled and tested, which is not in
// HEAD and would not travel with the push.
func TestWriteRefusesAnUntrackedFile(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatalf("adding an untracked file: %v", err)
	}
	if err := runWrite([]string{"--target", "ci"}); err == nil {
		t.Fatal("an untracked file did not count as dirty, so the gate certified a commit that lacks it")
	}
}

// TestVerifyRefusesADifferentCommit is the case the whole mechanism exists
// for: work continued after the gate ran.
func TestVerifyRefusesADifferentCommit(t *testing.T) {
	newRepo(t)
	if err := runWrite([]string{"--target", "push-gate"}); err != nil {
		t.Fatalf("runWrite: %v", err)
	}

	err := runVerify([]string{"--commit", strings.Repeat("a", 40)})
	if err == nil {
		t.Fatal("runVerify accepted a commit the receipt does not name")
	}
	// The message has to carry BOTH revisions, because "wrong commit" with
	// only one of them leaves the reader unable to tell which is stale.
	if !strings.Contains(err.Error(), "aaaaaaaaaaaa") {
		t.Errorf("the refusal does not name the commit being pushed: %v", err)
	}
}

// TestVerifyRefusesAStaleReceipt pins the one bound a commit hash cannot
// express.
//
// Everything a gate checks is a pure function of the tree except
// govulncheck, which reads a live advisory database. A month-old pass
// still describes the same code and no longer answers that one question,
// so the age bound exists for it alone and the message has to say so or
// it reads as arbitrary.
func TestVerifyRefusesAStaleReceipt(t *testing.T) {
	dir := newRepo(t)
	head := headOf(t)

	stale := Receipt{
		Commit:    head,
		Target:    "push-gate",
		WrittenAt: time.Now().UTC().Add(-MaxReceiptAge - time.Hour),
	}
	body, err := json.Marshal(stale)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", receiptName), body, 0o600); err != nil {
		t.Fatalf("writing a stale receipt: %v", err)
	}

	verifyErr := runVerify([]string{"--commit", head})
	if verifyErr == nil {
		t.Fatal("runVerify accepted a receipt past the age bound")
	}
	if !strings.Contains(verifyErr.Error(), "govulncheck") {
		t.Errorf("the refusal does not say why age matters, so it reads as arbitrary: %v", verifyErr)
	}
}

// TestVerifyRefusesWhenThereIsNoReceipt is the default state of a fresh
// clone, and the message is the only thing telling a new developer what to
// do, so it names the command.
func TestVerifyRefusesWhenThereIsNoReceipt(t *testing.T) {
	newRepo(t)

	err := runVerify([]string{"--commit", headOf(t)})
	if err == nil {
		t.Fatal("runVerify passed with no receipt at all")
	}
	if !strings.Contains(err.Error(), "make push-gate") {
		t.Errorf("the refusal does not name the command that fixes it: %v", err)
	}
}

// TestVerifyRefusesAnUnreadableReceipt covers the corrupt-file case, which
// must fail closed: a receipt nobody can parse is not evidence of a pass.
func TestVerifyRefusesAnUnreadableReceipt(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".git", receiptName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing a corrupt receipt: %v", err)
	}
	if err := runVerify([]string{"--commit", headOf(t)}); err == nil {
		t.Fatal("an unparseable receipt was treated as a pass")
	}
}

// TestWriteRequiresATarget keeps the two gates distinguishable. push-gate
// tolerates a failure confined to a flaky-packages.json package and ci does
// not, so a receipt that did not say which ran would let a tolerant pass be
// read as a strict one.
func TestWriteRequiresATarget(t *testing.T) {
	newRepo(t)
	if err := runWrite(nil); err == nil {
		t.Fatal("runWrite accepted a receipt that does not say which gate ran")
	}
}

// TestReceiptRecordsWhichGateRan proves the distinction above survives the
// round trip rather than only being validated on the way in.
func TestReceiptRecordsWhichGateRan(t *testing.T) {
	dir := newRepo(t)
	if err := runWrite([]string{"--target", "ci"}); err != nil {
		t.Fatalf("runWrite: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, ".git", receiptName))
	if err != nil {
		t.Fatalf("reading the receipt: %v", err)
	}
	var r Receipt
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("the receipt this command wrote does not parse: %v", err)
	}
	if r.Target != "ci" {
		t.Errorf("Target = %q, want the gate that actually ran", r.Target)
	}
	if r.Commit != headOf(t) {
		t.Errorf("Commit = %q, want HEAD", r.Commit)
	}
}
