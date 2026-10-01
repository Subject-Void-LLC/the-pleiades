//go:build unix

// Package loader: tests of the approval list.
package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// answering is a one-method program for fqcn whose invoke answers with a
// "who" fact of who, so a test can tell two builds apart by what ran.
func answering(t *testing.T, fqcn, who string) string {
	t.Helper()
	out := describeJSON(t, 0, external.DescribedMethod{Name: fqcn, Manifest: implemented(false)})
	return script(out, `printf '{"facts":{"who":"`+who+`"}}' >&3`)
}

// tryLoad loads dir and puts the registry back straight away, so one test
// can load the same names more than once, returning the error rather
// than failing.
func tryLoad(t *testing.T, dir string, opts Options) error {
	requireConfinement(t)
	t.Helper()
	restore := collection.SnapshotForTest()
	defer restore()
	_, err := Load(t.Context(), dir, opts)
	return err
}

// approval is a valid Approval of digest for program.
func approval(program, digest string) Approval {
	return Approval{Program: program, Digest: digest, ApprovedBy: "tester", ApprovedAt: time.Now().UTC().Format(time.RFC3339)}
}

// TestApproval_AnUnapprovedProgramNeverRuns proves a program missing from
// the approval list is refused before it runs at all, naming the command
// that approves it, and that approving it is what lets it load.
func TestApproval_AnUnapprovedProgramNeverRuns(t *testing.T) {
	dir := programDir(t)
	record := t.TempDir()
	marker := filepath.Join(record, "described")
	out := describeJSON(t, 0, external.DescribedMethod{Name: "loadertest.approve.run", Manifest: implemented(false)})
	path := writeUnapproved(t, dir, "prog", "#!/bin/sh\ntouch '"+marker+"'\n"+strings.TrimPrefix(script(out, `printf '{}' >&3`), "#!/bin/sh\n"))
	opts := testOptions()
	opts.testWritable = []string{record}

	err := tryLoad(t, dir, opts)
	if err == nil || !strings.Contains(err.Error(), "is not approved to run") || !strings.Contains(err.Error(), "pleiades collection approve prog") {
		t.Fatalf("Load = %v, want a refusal naming the approve command", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("an unapproved program ran (its describe touched the marker)")
	}

	approveProgram(t, path)
	if err := tryLoad(t, dir, opts); err != nil {
		t.Fatalf("Load after approval: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("the approved program's describe did not run, so the refusal above proves nothing")
	}
}

// TestApproval_AChangedBuildIsRefusedNamingBothDigests covers a program
// replaced after it was approved: refused at load naming both digests,
// and, when replaced after it was loaded, refused at invoke.
func TestApproval_AChangedBuildIsRefusedNamingBothDigests(t *testing.T) {
	dir := programDir(t)
	fqcn := "loadertest.changed.run"
	path := writeProgram(t, dir, "prog", answering(t, fqcn, "first"))
	approved, _ := inspectProgram(path)

	d := loadOne(t, dir, fqcn, testOptions())
	writeUnapproved(t, dir, "prog", answering(t, fqcn, "second"))
	changed, _ := inspectProgram(path)

	_, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
	if err == nil || !strings.Contains(err.Error(), approved) || !strings.Contains(err.Error(), changed) {
		t.Errorf("Invoke of a replaced program = %v, want a refusal naming %s and %s", err, approved, changed)
	}
	err = tryLoad(t, dir, testOptions())
	if err == nil || !strings.Contains(err.Error(), "changed since it was approved") || !strings.Contains(err.Error(), approved) || !strings.Contains(err.Error(), changed) {
		t.Errorf("Load of a replaced program = %v, want a refusal naming both digests", err)
	}
}

// TestApproval_TwoBuildsMayBeApproved covers a rolling upgrade: with both
// builds approved, either one loads.
func TestApproval_TwoBuildsMayBeApproved(t *testing.T) {
	dir := programDir(t)
	fqcn := "loadertest.rolling.run"
	writeProgram(t, dir, "prog", answering(t, fqcn, "old"))
	if err := tryLoad(t, dir, testOptions()); err != nil {
		t.Fatalf("the old build: %v", err)
	}
	writeProgram(t, dir, "prog", answering(t, fqcn, "new"))
	if err := tryLoad(t, dir, testOptions()); err != nil {
		t.Fatalf("the new build: %v", err)
	}
	list, err := ReadApprovals(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("ReadApprovals = %d approvals (%v), want both builds", len(list), err)
	}
	writeUnapproved(t, dir, "prog", answering(t, fqcn, "old"))
	if err := tryLoad(t, dir, testOptions()); err != nil {
		t.Errorf("the old build again, still approved: %v", err)
	}
}

// TestApproval_WithdrawingItStopsTheNextCall proves revoking an approval
// takes effect on the next call, without a reload, which is what lets a
// long-lived Runner be stopped from running a build.
func TestApproval_WithdrawingItStopsTheNextCall(t *testing.T) {
	dir := programDir(t)
	fqcn := "loadertest.revoke.run"
	path := writeProgram(t, dir, "prog", answering(t, fqcn, "only"))
	d := loadOne(t, dir, fqcn, testOptions())
	if _, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil); err != nil {
		t.Fatalf("Invoke before revoking: %v", err)
	}
	digest, _ := inspectProgram(path)
	if n, err := Revoke(dir, "prog", digest); err != nil || n != 1 {
		t.Fatalf("Revoke = %d, %v; want 1 removed", n, err)
	}
	_, err := d.Invoke(t.Context(), newRecordingContext(), newSSHDevice(), nil)
	if err == nil || !strings.Contains(err.Error(), "approval was withdrawn") {
		t.Errorf("Invoke after revoking = %v, want it refused", err)
	}
}

// TestApproval_AFaultyListIsRefusedWhole covers every way the list can be
// wrong. Each refuses the whole directory, including the program the list
// does approve correctly, since an unreadable entry might have been the
// one withdrawing an approval.
func TestApproval_AFaultyListIsRefusedWhole(t *testing.T) {
	good := "sha256:" + strings.Repeat("a", 64)
	for name, content := range map[string]string{
		"not JSON":        `{"version": 1, "approvals": [`,
		"an unknown key":  `{"version": 1, "approvals": [], "trusted": true}`,
		"another version": `{"version": 2, "approvals": []}`,
		"trailing data":   `{"version": 1, "approvals": []} {}`,
		"one bad digest": `{"version": 1, "approvals": [
			{"program": "x", "digest": "` + good + `", "approved_by": "a", "approved_at": "2026-09-18T00:00:00Z"},
			{"program": "y", "digest": "sha256:short", "approved_by": "a", "approved_at": "2026-09-18T00:00:00Z"}]}`,
		"a path as a name": `{"version": 1, "approvals": [{"program": "../x", "digest": "` + good + `", "approved_by": "a", "approved_at": "2026-09-18T00:00:00Z"}]}`,
		"no account":       `{"version": 1, "approvals": [{"program": "x", "digest": "` + good + `", "approved_by": "", "approved_at": "2026-09-18T00:00:00Z"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := programDir(t)
			writeProgram(t, dir, "prog", answering(t, "loadertest.faulty.run", "x"))
			if err := os.WriteFile(filepath.Join(dir, ApprovalFile), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tryLoad(t, dir, testOptions()); err == nil || !strings.Contains(err.Error(), "approval list") {
				t.Errorf("Load = %v, want the approval list refused", err)
			}
		})
	}
}

// TestApproval_TheListIsHeldToTheProgramsRules covers the list's own file:
// writable by others, or a symlink, it is refused like a program would be.
func TestApproval_TheListIsHeldToTheProgramsRules(t *testing.T) {
	dir := programDir(t)
	writeProgram(t, dir, "prog", answering(t, "loadertest.perm.run", "x"))
	list := filepath.Join(dir, ApprovalFile)
	if err := os.Chmod(list, 0o620); err != nil { // #nosec G302 -- the fixture under test
		t.Fatal(err)
	}
	if err := tryLoad(t, dir, testOptions()); err == nil || !strings.Contains(err.Error(), "group- or world-writable") {
		t.Errorf("a group-writable list = %v, want it refused", err)
	}

	other := filepath.Join(t.TempDir(), "elsewhere.json")
	data, _ := os.ReadFile(list) // #nosec G304 -- the test's own fixture
	if err := os.WriteFile(other, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(list); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, list); err != nil {
		t.Fatal(err)
	}
	if err := tryLoad(t, dir, testOptions()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a symlinked list = %v, want it refused", err)
	}
}

// TestApproval_ApproveAndRevoke covers the list's writers: an approval is
// validated, recorded once however often it is given, and revoked by
// digest or all at once.
func TestApproval_ApproveAndRevoke(t *testing.T) {
	dir := programDir(t)
	a := approval("prog", "sha256:"+strings.Repeat("1", 64))
	b := approval("prog", "sha256:"+strings.Repeat("2", 64))
	for _, x := range []Approval{a, a, b} {
		if err := Approve(dir, x); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := ReadApprovals(dir); len(list) != 2 {
		t.Fatalf("approving the same build twice recorded %d approvals, want 2", len(list))
	}
	if info, err := os.Stat(filepath.Join(dir, ApprovalFile)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the list's mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
	if err := Approve(dir, approval("prog", "sha256:nothex")); err == nil {
		t.Error("an approval with a malformed digest was accepted")
	}
	if n, _ := Revoke(dir, "prog", a.Digest); n != 1 {
		t.Errorf("revoking one build removed %d", n)
	}
	if n, _ := Revoke(dir, "prog", ""); n != 1 {
		t.Errorf("revoking the rest removed %d", n)
	}
	if list, _ := ReadApprovals(dir); len(list) != 0 {
		t.Errorf("approvals left after revoking all: %+v", list)
	}
}

// TestApproval_TheVerifiedFileIsTheOneThatRuns is the swap race. After a
// program is verified and found approved, and before it starts, another
// program is renamed over its name. The verified build must be what runs,
// since the run goes through the file that was checked. The control starts
// by path in the same window and runs the replacement, which is what shows
// the window is real.
func TestApproval_TheVerifiedFileIsTheOneThatRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		byPath bool
		want   string
	}{
		{name: "through the verified file", byPath: false, want: "approved"},
		{name: "control: by path", byPath: true, want: "swapped-in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := programDir(t)
			fqcn := "loadertest.swap.run"
			path := writeProgram(t, dir, "prog", answering(t, fqcn, "approved"))
			spare := t.TempDir()
			impostor := writeUnapproved(t, spare, "impostor", answering(t, fqcn, "swapped-in"))

			opts := testOptions()
			opts.execByPath = tc.byPath
			opts.beforeStart = func() {
				if err := os.Rename(impostor, path); err != nil {
					t.Errorf("swapping the program: %v", err)
				}
			}
			d := loadOne(t, dir, fqcn, opts)
			rc := newRecordingContext()
			if _, err := d.Invoke(t.Context(), rc, newSSHDevice(), nil); err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if got := rc.stats["who"]; got != tc.want {
				t.Errorf("the build that ran answered %v, want %s", got, tc.want)
			}
		})
	}
}
