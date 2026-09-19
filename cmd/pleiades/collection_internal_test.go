// Package main: in-process tests of the `pleiades collection` commands.
//
// cmd/pleiades's own suite drives the real binary as a subprocess, which is
// how a CLI should be tested and is what collection_test.go does for these
// same commands. It proves what an operator sees; it cannot show which
// branches ran, since `go test -cover` counts only this process. These
// tests call the commands here, so the approval list's own decisions are
// covered as well as demonstrated.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
)

// describingProgram answers describe with two methods, between them
// carrying every field printDescription has a branch for: a summary, the
// capabilities and transports a method needs, check support, and both
// answers to whether it can be undone. It ignores every other argument,
// so nothing it is asked to run does anything.
const describingProgram = `#!/bin/sh
if [ "$1" = describe ]; then
  printf '%s' '{"protocol":1,"methods":[
    {"name":"clitest.inproc.full","manifest":{"status":"implemented","supportsCheck":true,
      "requiredCapabilities":["SSHTransportCapable"],"supportedTransports":["ssh"],
      "doc":{"summary":"writes a note"},"reversibility":{"reversible":false,"notes":"a note cannot be unwritten"}}},
    {"name":"clitest.inproc.bare","manifest":{"status":"implemented","supportsCheck":false,
      "reversibility":{"reversible":true}}}]}'
fi
`

// collectionFixture writes the describing program into a directory the
// loader will accept and points PLEIADES_COLLECTIONS_DIR at it.
func collectionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note"), []byte(describingProgram), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}
	t.Setenv(collectionsDirEnv, dir)
	return dir
}

// answering makes confirm read answer for the rest of the test.
func answering(t *testing.T, answer string) {
	t.Helper()
	original := approvalPrompt
	approvalPrompt = strings.NewReader(answer)
	t.Cleanup(func() { approvalPrompt = original })
}

// captureRun runs fn with stdout captured into out, since these commands
// print what an operator reads and return only whether it worked.
func captureRun(t *testing.T, out *string, fn func() error) error {
	t.Helper()
	var err error
	*out = captureStdout(t, func() { err = fn() })
	return err
}

// approvedDigests returns the digests recorded for program.
func approvedDigests(t *testing.T, dir, program string) []string {
	t.Helper()
	approvals, err := loader.ReadApprovals(dir)
	if err != nil {
		t.Fatalf("reading the approval list: %v", err)
	}
	var digests []string
	for _, a := range approvals {
		if a.Program == program {
			digests = append(digests, a.Digest)
		}
	}
	return digests
}

// TestRunCollectionApprove_ShowsTheBuildAndRecordsTheAnswer covers the
// approval an operator is asked for: the program is run, confined, to be
// described, everything it says about itself is shown, and only "y"
// records the build it showed. "n" records nothing and fails, so a script
// that pipes the wrong thing does not approve code by accident.
func TestRunCollectionApprove_ShowsTheBuildAndRecordsTheAnswer(t *testing.T) {
	dir := collectionFixture(t)

	answering(t, "n\n")
	var refusedOut string
	err := captureRun(t, &refusedOut, func() error { return runCollection([]string{"approve", "note"}) })
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("answering n = %v, want the approval refused", err)
	}
	for _, want := range []string{
		"clitest.inproc.full", "writes a note",
		"needs:      SSHTransportCapable", "transports: ssh",
		"check mode: supported", "reversible: no (a note cannot be unwritten)",
		"clitest.inproc.bare", "needs:      none", "transports: none",
		"check mode: not supported", "reversible: yes",
	} {
		if !strings.Contains(refusedOut, want) {
			t.Errorf("the description does not show %q:\n%s", want, refusedOut)
		}
	}
	if got := approvedDigests(t, dir, "note"); len(got) != 0 {
		t.Fatalf("answering n recorded %v", got)
	}

	answering(t, "y\n")
	var approvedOut string
	if err := captureRun(t, &approvedOut, func() error { return runCollection([]string{"approve", "note"}) }); err != nil {
		t.Fatalf("answering y = %v", err)
	}
	digests := approvedDigests(t, dir, "note")
	if len(digests) != 1 || !strings.HasPrefix(digests[0], "sha256:") {
		t.Fatalf("approved %v, want one sha256 digest", digests)
	}
	if !strings.Contains(approvedOut, "approved note ("+digests[0]+")") {
		t.Errorf("the approval was not reported with its digest:\n%s", approvedOut)
	}
	// The digest recorded is the one the operator was shown, or they
	// approved one build and ran another.
	if !strings.Contains(refusedOut, digests[0]) {
		t.Errorf("the digest shown and the digest recorded differ:\n%s", refusedOut)
	}
}

// TestRunCollectionApprove_ByDigestRunsNothing covers approving a build
// that is not here yet, for an image build that installs the program
// later: the digest is recorded without the program being run, so it
// works for a name no file answers to, and a digest that is not one is
// refused rather than stored.
func TestRunCollectionApprove_ByDigestRunsNothing(t *testing.T) {
	dir := collectionFixture(t)
	next := "sha256:" + strings.Repeat("b", 64)

	var out string
	if err := captureRun(t, &out, func() error {
		return runCollection([]string{"approve", "not-installed-yet", "--digest", next})
	}); err != nil {
		t.Fatalf("approving a program that is not here = %v", err)
	}
	if got := approvedDigests(t, dir, "not-installed-yet"); len(got) != 1 || got[0] != next {
		t.Errorf("recorded %v, want %s", got, next)
	}

	if err := captureRun(t, &out, func() error {
		return runCollection([]string{"approve", "note", "--digest", "sha256:nope"})
	}); err == nil {
		t.Error("a malformed digest was approved")
	}
	if got := approvedDigests(t, dir, "note"); len(got) != 0 {
		t.Errorf("a malformed digest recorded %v", got)
	}
}

// TestRunCollectionApprove_YesSkipsTheQuestion covers the flag a
// provisioning run uses: the build is still described, and approved with
// nothing to answer, so a run with no terminal does not hang.
func TestRunCollectionApprove_YesSkipsTheQuestion(t *testing.T) {
	dir := collectionFixture(t)
	answering(t, "") // nothing to read: an unasked question is the point

	var out string
	if err := captureRun(t, &out, func() error { return runCollection([]string{"approve", "note", "--yes"}) }); err != nil {
		t.Fatalf("approve --yes = %v", err)
	}
	if !strings.Contains(out, "clitest.inproc.full") {
		t.Errorf("--yes skipped the description as well as the question:\n%s", out)
	}
	if got := approvedDigests(t, dir, "note"); len(got) != 1 {
		t.Fatalf("approve --yes recorded %v, want one build", got)
	}
}

// TestRunCollectionListAndRevoke covers the other two commands against a
// list with two builds of one program: list prints both, revoke takes the
// one a digest names and then the rest, and revoking what is not there
// fails rather than reporting that it removed nothing.
func TestRunCollectionListAndRevoke(t *testing.T) {
	dir := collectionFixture(t)
	first := "sha256:" + strings.Repeat("a", 64)
	second := "sha256:" + strings.Repeat("b", 64)
	var out string
	for _, digest := range []string{first, second} {
		if err := captureRun(t, &out, func() error {
			return runCollection([]string{"approve", "note", "--digest", digest})
		}); err != nil {
			t.Fatalf("approving %s: %v", digest, err)
		}
	}

	if err := captureRun(t, &out, func() error { return runCollection([]string{"list"}) }); err != nil {
		t.Fatalf("list = %v", err)
	}
	if strings.Count(out, "note  sha256:") != 2 || !strings.Contains(out, "approved by ") {
		t.Errorf("list does not show both builds and who approved them:\n%s", out)
	}

	if err := captureRun(t, &out, func() error {
		return runCollection([]string{"revoke", "note", "--digest", second})
	}); err != nil || !strings.Contains(out, "revoked 1 approval(s) of note") {
		t.Fatalf("revoking one build = %v:\n%s", err, out)
	}
	if got := approvedDigests(t, dir, "note"); len(got) != 1 || got[0] != first {
		t.Fatalf("after revoking one build the list holds %v, want %s", got, first)
	}

	if err := captureRun(t, &out, func() error { return runCollection([]string{"revoke", "note"}) }); err != nil {
		t.Fatalf("revoking the rest = %v", err)
	}
	if got := approvedDigests(t, dir, "note"); len(got) != 0 {
		t.Fatalf("revoking every build left %v", got)
	}
	if err := captureRun(t, &out, func() error { return runCollection([]string{"revoke", "note"}) }); err == nil {
		t.Error("revoking what is not there succeeded")
	}

	if err := captureRun(t, &out, func() error { return runCollection([]string{"list"}) }); err != nil {
		t.Fatalf("list with nothing approved = %v", err)
	}
	if !strings.Contains(out, "no approved builds") {
		t.Errorf("an empty list prints %q", out)
	}
}

// TestRunCollection_RefusesWhatItCannotAct is the family's own dispatch:
// no subcommand and an unknown one are refused with the usage block, help
// is not an error, and every command says which variable to set when the
// collections directory is not named, since none of them can guess it.
func TestRunCollection_RefusesWhatItCannotAct(t *testing.T) {
	t.Setenv(collectionsDirEnv, "")
	var out string

	if err := captureRun(t, &out, func() error { return runCollection(nil) }); err != errUnknownCommand {
		t.Errorf("no subcommand = %v, want errUnknownCommand", err)
	}
	if err := captureRun(t, &out, func() error { return runCollection([]string{"inspect"}) }); err != errUnknownCommand {
		t.Errorf("an unknown subcommand = %v, want errUnknownCommand", err)
	}
	if err := captureRun(t, &out, func() error { return runCollection([]string{"--help"}) }); err != nil {
		t.Errorf("--help = %v, want it to succeed", err)
	}

	for _, args := range [][]string{
		{"list"},
		{"approve", "note", "--digest", "sha256:" + strings.Repeat("c", 64)},
		{"revoke", "note"},
	} {
		err := captureRun(t, &out, func() error { return runCollection(args) })
		if err == nil || !strings.Contains(err.Error(), collectionsDirEnv+" is not set") {
			t.Errorf("%v with no directory named = %v, want it to name %s", args, err, collectionsDirEnv)
		}
	}
}
