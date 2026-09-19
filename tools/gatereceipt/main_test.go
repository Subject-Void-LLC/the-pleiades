// These tests drive the receipt against REAL git repositories created per
// case, rather than against a fake, because every decision this command
// makes is a question it asks git: what is HEAD, is the tree dirty, where
// does .git live, does one commit contain another, what does this tag
// point at. A fake would be asserting the test's own answers.
//
// The pre-push half is driven through a real pipe on os.Stdin carrying the
// exact lines git writes to a hook, for the same reason: that format is the
// contract, and a test that called the decision function directly with
// fields it made up would not notice the day the parsing broke.
package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitIn runs one git command in a fixture repository.
//
// The environment is pinned rather than inherited: a test must not depend
// on the developer's own git identity, and an inherited core.hooksPath
// would run this repository's real pre-push inside a fixture.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo builds a real repository with one commit and chdirs into it.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	gitIn(t, dir, "init", "--quiet")
	gitIn(t, dir, "config", "core.hooksPath", "")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatalf("writing a fixture file: %v", err)
	}
	gitIn(t, dir, "add", "a.txt")
	gitIn(t, dir, "commit", "--quiet", "-m", "first")

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

// addCommit puts one more commit on the branch and returns it.
func addCommit(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content+"\n"), 0o600); err != nil {
		t.Fatalf("writing a fixture file: %v", err)
	}
	gitIn(t, dir, "commit", "--quiet", "-am", content)
	return headOf(t)
}

func headOf(t *testing.T) string {
	t.Helper()
	sha, err := headCommit()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return sha
}

// gateRun is the arguments the Makefile builds: the target, plus where the
// gate STARTED, captured before the first check rather than after the last
// one. Every test that writes a receipt goes through this, so none of them
// can accidentally prove the thing this command refuses to assume.
func gateRun(t *testing.T, target string) []string {
	t.Helper()
	dirty, err := workingTreeDirty()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	clean := "yes"
	if dirty {
		clean = "no"
	}
	return []string{"--target", target, "--started-at", headOf(t), "--started-clean", clean}
}

// pushLines puts the lines git gives a pre-push hook on os.Stdin, through a
// real pipe, which is what git uses.
func pushLines(t *testing.T, lines string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	go func() {
		_, _ = io.WriteString(w, lines)
		_ = w.Close()
	}()
	saved := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = saved
		_ = r.Close()
	})
}

// receiptPath is where a fixture's receipt lives.
func receiptPath(dir string) string { return filepath.Join(dir, ".git", receiptName) }

// zeros is what git writes for an object that does not exist: a ref being
// deleted locally, or one the remote does not have yet.
const zeros = "0000000000000000000000000000000000000000"

// TestWriteThenVerify is the whole contract in one pass: a gate that
// passed lets that commit through, and nothing else.
func TestWriteThenVerify(t *testing.T) {
	newRepo(t)

	if err := runWrite(gateRun(t, "push-gate")); err != nil {
		t.Fatalf("runWrite on a clean tree: %v", err)
	}
	if err := runVerify([]string{"--commit", headOf(t)}); err != nil {
		t.Fatalf("runVerify for the commit just recorded: %v", err)
	}
}

// TestWriteRefusesACommitMadeWhileTheGateRan is the hole this command had
// for its first month, and the reason write is told where the gate started.
//
// The receipt named HEAD as it was when the last line of a twenty minute
// gate ran. A commit made inside that window therefore collected a receipt
// for a tree nothing had examined, and nothing at the end could notice,
// because committing leaves the tree CLEAN: the dirty-tree refusal saw a
// tidy repository and wrote the receipt happily. It was reproduced exactly
// this way, and the hook then reported the unexamined commit as passed.
func TestWriteRefusesACommitMadeWhileTheGateRan(t *testing.T) {
	dir := newRepo(t)
	examined := headOf(t)
	started := gateRun(t, "push-gate")

	// The commit a developer makes while the suite runs: a doc tweak, an
	// amended message, the end-of-session ritual.
	made := addCommit(t, dir, "two")

	err := runWrite(started)
	if err == nil {
		t.Fatal("a receipt was written for a commit the gate never examined")
	}
	// Both revisions, because the developer's next question is which one
	// the gate actually looked at.
	if !strings.Contains(err.Error(), short(examined)) || !strings.Contains(err.Error(), short(made)) {
		t.Errorf("the refusal does not name both commits: %v", err)
	}
	if _, statErr := os.Stat(receiptPath(dir)); !os.IsNotExist(statErr) {
		t.Error("a receipt file exists, so the push would have been allowed")
	}
}

// TestWriteRequiresWhereTheGateStarted keeps the fix above from being
// bypassed by a caller that simply omits the flags. What HEAD was twenty
// minutes ago is the one fact this command cannot observe, so a default
// would be a guess in the permissive direction.
func TestWriteRequiresWhereTheGateStarted(t *testing.T) {
	newRepo(t)

	err := runWrite([]string{"--target", "ci"})
	if err == nil {
		t.Fatal("runWrite issued a receipt without being told where the gate started")
	}
	if !strings.Contains(err.Error(), "make ci") {
		t.Errorf("the refusal does not name how to run the gate properly: %v", err)
	}
}

// TestWriteRefusesADirtyTree is the property that makes this stricter than
// the hook it replaces.
//
// A receipt names a commit. A gate run against a tree with uncommitted
// edits proved something about code that is not in any commit, so issuing
// one from it would make the receipt's own sentence false. The old
// arrangement had exactly this gap and nothing noticed: it ran the suite
// over the working tree and then pushed commits.
func TestWriteRefusesADirtyTree(t *testing.T) {
	dir := newRepo(t)
	started := gateRun(t, "push-gate")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatalf("dirtying the tree: %v", err)
	}

	err := runWrite(started)
	if err == nil {
		t.Fatal("runWrite issued a receipt from a dirty tree, so the receipt names a commit nobody tested")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Errorf("refusal does not say why: %v", err)
	}
	if _, statErr := os.Stat(receiptPath(dir)); !os.IsNotExist(statErr) {
		t.Error("a receipt file was written despite the refusal")
	}
}

// TestWriteRefusesATreeThatWasAlreadyDirty covers the other end of the
// window: edits present when the gate STARTED, reverted or stashed before
// it finished. HEAD never moved and the tree is clean by the time the
// receipt would be written, so only the starting state can tell anyone that
// the suite examined code no commit holds.
func TestWriteRefusesATreeThatWasAlreadyDirty(t *testing.T) {
	dir := newRepo(t)
	head := headOf(t)

	err := runWrite([]string{"--target", "ci", "--started-at", head, "--started-clean", "no"})
	if err == nil {
		t.Fatal("a gate that started against uncommitted edits still wrote a receipt")
	}
	if !strings.Contains(err.Error(), "already had uncommitted changes") {
		t.Errorf("the refusal does not say which end of the run was dirty: %v", err)
	}
	if _, statErr := os.Stat(receiptPath(dir)); !os.IsNotExist(statErr) {
		t.Error("a receipt file was written despite the refusal")
	}
}

// TestWriteRefusesAnUntrackedFile covers the half of "dirty" a plain diff
// is blind to: a new file the gate compiled and tested, which is not in
// HEAD and would not travel with the push.
func TestWriteRefusesAnUntrackedFile(t *testing.T) {
	dir := newRepo(t)
	started := gateRun(t, "ci")
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package p\n"), 0o600); err != nil {
		t.Fatalf("adding an untracked file: %v", err)
	}
	if err := runWrite(started); err == nil {
		t.Fatal("an untracked file did not count as dirty, so the gate certified a commit that lacks it")
	}
}

// TestWriteRefusesAnUnknownTarget keeps the field meaningful at the point
// it is filled in. A receipt exists partly to say whether the pass was
// strict, and a target neither command understands cannot be read as
// either answer.
func TestWriteRefusesAnUnknownTarget(t *testing.T) {
	newRepo(t)
	args := gateRun(t, "sort-of-a-gate")
	if err := runWrite(args); err == nil {
		t.Fatal("a receipt was written naming a gate that does not exist")
	}
}

// TestVerifyRefusesADifferentCommit is the case the whole mechanism exists
// for: work continued after the gate ran.
func TestVerifyRefusesADifferentCommit(t *testing.T) {
	dir := newRepo(t)
	if err := runWrite(gateRun(t, "push-gate")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}
	gated := headOf(t)
	later := addCommit(t, dir, "two")

	err := runVerify([]string{"--commit", later})
	if err == nil {
		t.Fatal("runVerify accepted a commit the receipt does not name")
	}
	// The message has to carry BOTH revisions, because "wrong commit" with
	// only one of them leaves the reader unable to tell which is stale.
	if !strings.Contains(err.Error(), short(later)) || !strings.Contains(err.Error(), short(gated)) {
		t.Errorf("the refusal does not name both commits: %v", err)
	}
}

// TestVerifyAcceptsAnAnnotatedTagAtTheGatedCommit covers the refusal that
// used to arrive at the worst possible moment.
//
// Git does not hand a hook the commit for a tag ref; it hands it the tag
// OBJECT's name, which is a different hash. So `git tag -a` at the very
// commit that just passed, followed by a push of that tag, was refused as
// "a receipt for a different commit", and the only remedy anyone reaches
// for at that point is --no-verify, which is the habit this gate cannot
// afford to teach. A lightweight tag was accepted the whole time, which
// makes the refusal look arbitrary as well as wrong.
func TestVerifyAcceptsAnAnnotatedTagAtTheGatedCommit(t *testing.T) {
	dir := newRepo(t)
	if err := runWrite(gateRun(t, "ci")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}
	gitIn(t, dir, "tag", "-a", "v1", "-m", "release one")
	tagObject := gitIn(t, dir, "rev-parse", "v1")
	if tagObject == headOf(t) {
		t.Fatal("the fixture did not produce an annotated tag, so this proves nothing")
	}

	pushLines(t, "refs/tags/v1 "+tagObject+" refs/tags/v1 "+zeros+"\n")
	if err := runVerify([]string{"--push-stdin"}); err != nil {
		t.Errorf("a tag cut at the gated commit was refused: %v", err)
	}
}

// TestVerifyRefusesAnObjectThatIsNotACommit fails closed on the other side
// of that peeling: a tag pointing at a blob resolves to no commit at all,
// so no gate can have examined it.
func TestVerifyRefusesAnObjectThatIsNotACommit(t *testing.T) {
	dir := newRepo(t)
	if err := runWrite(gateRun(t, "ci")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}
	blob := gitIn(t, dir, "hash-object", "-w", "a.txt")

	pushLines(t, "refs/tags/thing "+blob+" refs/tags/thing "+zeros+"\n")
	err := runVerify([]string{"--push-stdin"})
	if err == nil {
		t.Fatal("an object that is not a commit was accepted")
	}
	if !strings.Contains(err.Error(), "does not resolve to a commit") {
		t.Errorf("the refusal does not say what was wrong with it: %v", err)
	}
}

// TestVerifyAcceptsHistoryTheGatedPushAlreadyCarries covers a ref created
// at a commit BEHIND the gated one.
//
// A push sends every commit its tip can reach, so those commits are
// travelling under the receipt regardless; a tag cut at last week's release
// or a topic branch left behind adds no code the gate has not already seen
// in the tip's history. Refusing them denied nothing and taught
// --no-verify, which is the one habit that turns this gate off for good.
func TestVerifyAcceptsHistoryTheGatedPushAlreadyCarries(t *testing.T) {
	dir := newRepo(t)
	older := headOf(t)
	addCommit(t, dir, "two")
	if err := runWrite(gateRun(t, "push-gate")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}

	pushLines(t, "refs/tags/old "+older+" refs/tags/old "+zeros+"\n")
	if err := runVerify([]string{"--push-stdin"}); err != nil {
		t.Errorf("a new ref inside the gated history was refused: %v", err)
	}
}

// TestVerifyRefusesARefMovedBackwards is what keeps the allowance above
// honest. Sending history that travels with a gated tip is one thing;
// repointing a ref the remote already has at an older commit is another,
// because the remote is then serving a tree no gate examined as that ref's
// tip.
func TestVerifyRefusesARefMovedBackwards(t *testing.T) {
	dir := newRepo(t)
	older := headOf(t)
	newer := addCommit(t, dir, "two")
	if err := runWrite(gateRun(t, "push-gate")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}

	pushLines(t, "refs/heads/main "+older+" refs/heads/main "+newer+"\n")
	err := runVerify([]string{"--push-stdin"})
	if err == nil {
		t.Fatal("a ref was allowed to move back onto a commit no gate examined")
	}
	if !strings.Contains(err.Error(), short(older)) || !strings.Contains(err.Error(), short(newer)) {
		t.Errorf("the refusal does not name where the ref is and where it would go: %v", err)
	}
}

// TestVerifyRefusesWhenTheRemoteCommitIsUnknownHere is the fail-closed
// direction of that same check. Whether a ref moves backwards is a question
// about an object this clone may never have fetched, and an answer nobody
// has is not a yes.
func TestVerifyRefusesWhenTheRemoteCommitIsUnknownHere(t *testing.T) {
	dir := newRepo(t)
	older := headOf(t)
	addCommit(t, dir, "two")
	if err := runWrite(gateRun(t, "ci")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}

	unfetched := strings.Repeat("d", 40)
	pushLines(t, "refs/heads/main "+older+" refs/heads/main "+unfetched+"\n")
	err := runVerify([]string{"--push-stdin"})
	if err == nil {
		t.Fatal("a ref was allowed on an ancestry question this clone cannot answer")
	}
	if !strings.Contains(err.Error(), "does not have the remote's") {
		t.Errorf("the refusal does not say what could not be decided: %v", err)
	}
}

// TestVerifyPassesAPushThatOnlyDeletesRefs needs no receipt at all: a
// deletion sends no code, so there is nothing any gate could have examined.
// Requiring one would mean running a twenty minute suite to tidy up a
// merged branch.
func TestVerifyPassesAPushThatOnlyDeletesRefs(t *testing.T) {
	newRepo(t)

	pushLines(t, "(delete) "+zeros+" refs/heads/gone "+headOf(t)+"\n")
	if err := runVerify([]string{"--push-stdin"}); err != nil {
		t.Errorf("a push that only deletes refs was refused: %v", err)
	}
}

// TestVerifyAnswersEveryRefBeforeRefusing matters on a push of several
// refs. Stopping at the first refusal would have a developer discover the
// second one after the next twenty minute gate run, and the third after the
// one following that.
func TestVerifyAnswersEveryRefBeforeRefusing(t *testing.T) {
	dir := newRepo(t)
	if err := runWrite(gateRun(t, "push-gate")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}
	first := addCommit(t, dir, "two")
	second := addCommit(t, dir, "three")

	pushLines(t, "refs/heads/one "+first+" refs/heads/one "+zeros+"\n"+
		"refs/heads/two "+second+" refs/heads/two "+zeros+"\n")
	err := runVerify([]string{"--push-stdin"})
	if err == nil {
		t.Fatal("two ungated refs were accepted")
	}
	if !strings.Contains(err.Error(), "refs/heads/one") || !strings.Contains(err.Error(), "refs/heads/two") {
		t.Errorf("the refusal covers only some of the refs being pushed: %v", err)
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
	writeRawReceipt(t, dir, Receipt{
		Commit:    head,
		Target:    "push-gate",
		WrittenAt: time.Now().UTC().Add(-MaxReceiptAge - time.Hour),
	})

	verifyErr := runVerify([]string{"--commit", head})
	if verifyErr == nil {
		t.Fatal("runVerify accepted a receipt past the age bound")
	}
	if !strings.Contains(verifyErr.Error(), "govulncheck") {
		t.Errorf("the refusal does not say why age matters, so it reads as arbitrary: %v", verifyErr)
	}
}

// TestVerifyRefusesAReceiptDatedAhead covers the direction the age bound
// could not see. An age is a subtraction, and a receipt dated in the future
// produces a negative one, which is below every bound: such a receipt never
// expired, so a clock that stepped backwards, or a hand-edited timestamp,
// bought an unlimited pass.
func TestVerifyRefusesAReceiptDatedAhead(t *testing.T) {
	dir := newRepo(t)
	head := headOf(t)
	writeRawReceipt(t, dir, Receipt{
		Commit:    head,
		Target:    "ci",
		WrittenAt: time.Now().UTC().Add(48 * time.Hour),
	})

	err := runVerify([]string{"--commit", head})
	if err == nil {
		t.Fatal("a receipt dated two days ahead was accepted, and would have been forever")
	}
	if !strings.Contains(err.Error(), "future") {
		t.Errorf("the refusal does not say what is wrong with the date: %v", err)
	}
}

// TestVerifyToleratesASmallClockStep is the other half of that bound. NTP
// stepping a laptop's clock back a few seconds while a twenty minute gate
// runs is ordinary, and must not cost the run.
func TestVerifyToleratesASmallClockStep(t *testing.T) {
	dir := newRepo(t)
	head := headOf(t)
	writeRawReceipt(t, dir, Receipt{
		Commit:    head,
		Target:    "ci",
		WrittenAt: time.Now().UTC().Add(MaxClockSkew / 2),
	})

	if err := runVerify([]string{"--commit", head}); err != nil {
		t.Errorf("a receipt a few minutes ahead of the clock was refused: %v", err)
	}
}

// TestVerifyRefusesAReceiptThatDoesNotSayWhichGateRan covers the field
// nothing used to read. `write` has always required it, so a receipt
// without one was either hand-made or from a version that is gone, and in
// both cases it cannot say whether the pass was the strict one.
func TestVerifyRefusesAReceiptThatDoesNotSayWhichGateRan(t *testing.T) {
	dir := newRepo(t)
	head := headOf(t)

	for _, tc := range []struct {
		name   string
		target string
		want   string
	}{
		{name: "no target at all", target: "", want: "does not say which gate ran"},
		{name: "a gate nobody has", target: "make-it-pass", want: "not a gate this repository has"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeRawReceipt(t, dir, Receipt{Commit: head, Target: tc.target, WrittenAt: time.Now().UTC()})
			err := runVerify([]string{"--commit", head})
			if err == nil {
				t.Fatalf("a receipt naming %q was accepted", tc.target)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to say %q", err, tc.want)
			}
		})
	}
}

// TestVerifyRefusesAReceiptNamingSomethingThatIsNotACommit fails closed on
// a hand-edited file, and is also what keeps a value out of git's own
// revision arguments: `rev-list --stdin` honours pseudo-options such as
// --all on that stream, so an unvalidated commit field could widen the
// question being asked instead of answering it.
func TestVerifyRefusesAReceiptNamingSomethingThatIsNotACommit(t *testing.T) {
	dir := newRepo(t)
	writeRawReceipt(t, dir, Receipt{Commit: "--all", Target: "ci", WrittenAt: time.Now().UTC()})

	if err := runVerify([]string{"--commit", headOf(t)}); err == nil {
		t.Fatal("a receipt whose commit is not a commit was treated as evidence")
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
	if err := os.WriteFile(receiptPath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing a corrupt receipt: %v", err)
	}
	if err := runVerify([]string{"--commit", headOf(t)}); err == nil {
		t.Fatal("an unparseable receipt was treated as a pass")
	}
}

// TestReceiptRecordsWhichGateRan proves the distinction between a strict
// and a tolerant pass survives the round trip rather than only being
// validated on the way in.
func TestReceiptRecordsWhichGateRan(t *testing.T) {
	dir := newRepo(t)
	if err := runWrite(gateRun(t, "ci")); err != nil {
		t.Fatalf("runWrite: %v", err)
	}

	body, err := os.ReadFile(receiptPath(dir))
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

// TestCheckRefSaysWhetherThePassWasStrict is why the target is recorded at
// all. push-gate tolerates a test failure that passes when re-run alone and
// ci does not, and the line a developer reads at push time is the only
// place that distinction is ever put in front of them.
func TestCheckRefSaysWhetherThePassWasStrict(t *testing.T) {
	commit := strings.Repeat("ab", 20)
	for target, want := range map[string]string{"ci": "strict", "push-gate": "tolerant"} {
		line, err := checkRef(Receipt{Commit: commit, Target: target, WrittenAt: time.Now()}, pushRef{local: commit})
		if err != nil {
			t.Fatalf("checkRef on the gated commit: %v", err)
		}
		if !strings.Contains(line, target) || !strings.Contains(line, want) {
			t.Errorf("line = %q, want it to name %s and say it was %s", line, target, want)
		}
	}
}

// TestReadPushRefsKeepsWhatGitSends covers the parsing of the hook's own
// contract: four fields per ref, deletions dropped because they send no
// code, and a line that is not that shape refused rather than guessed at.
func TestReadPushRefsKeepsWhatGitSends(t *testing.T) {
	sha := strings.Repeat("c", 40)
	refs, err := readPushRefs(strings.NewReader(
		"refs/heads/main " + sha + " refs/heads/main " + zeros + "\n" +
			"(delete) " + zeros + " refs/heads/gone " + sha + "\n\n"))
	if err != nil {
		t.Fatalf("readPushRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].name != "refs/heads/main" || refs[0].local != sha || refs[0].remote != zeros {
		t.Fatalf("refs = %+v, want the one ref that sends something", refs)
	}

	if _, err := readPushRefs(strings.NewReader("refs/heads/main " + sha + "\n")); err == nil {
		t.Error("a line that is not git's four fields was parsed anyway")
	}
}

// writeRawReceipt puts a receipt on disk without going through write, which
// is the only way to test what verify does with one write would refuse to
// produce: stale, dated ahead, or naming a gate nobody has.
func writeRawReceipt(t *testing.T, dir string, r Receipt) {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("encoding a fixture receipt: %v", err)
	}
	if err := os.WriteFile(receiptPath(dir), body, 0o600); err != nil {
		t.Fatalf("writing a fixture receipt: %v", err)
	}
}
