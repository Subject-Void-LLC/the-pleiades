// Tests that a pr-check receipt is written only over a report that passed
// at the commit it names, against real git repositories and real reports:
// records written through each producer's own writer and merged by
// tools/internal/prreport, as make pr-check leaves them. And that verify
// counts a receipt's age from its run's oldest record, which make pr-check
// LEGS=... can keep from an earlier run.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// reportFor writes a records directory outside the repository, so it never
// dirties the tree, and merges it into report.json there as make pr-check's
// last step does, returning its path. Every record is stamped with stamp;
// the fast pass began at oldest; a mac leg is stamped with legCommit.
func reportFor(t *testing.T, commit string, stamp flakegate.Stamp, legCommit string, oldest time.Time) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := prreport.Begin(dir, commit, []string{"fast", "mac", "govulncheck"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	fast := flakegate.Run{Label: "testgate -full", OS: "linux", Packages: []string{"x/a"}, Verdict: flakegate.VerdictPass,
		Commit: stamp.Commit, Clean: stamp.Clean, Started: oldest}
	if err := flakegate.WriteRun(filepath.Join(dir, "fast.json"), fast); err != nil {
		t.Fatal(err)
	}
	mac := oscheck.LegRecord{Tool: "maccheck", OS: "macOS", Target: "mac", Commit: legCommit, Started: time.Now(), Verdict: flakegate.VerdictPass}
	if err := oscheck.WriteLegRecord(filepath.Join(dir, "mac.json"), mac); err != nil {
		t.Fatal(err)
	}
	if err := prreport.WriteStep(dir, "govulncheck", 0, "", stamp, time.Now()); err != nil {
		t.Fatal(err)
	}
	zero := 0
	r, err := prreport.Merge(dir, prreport.MergeOptions{ExitCode: &zero, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, prreport.ReportFile)
	if err := prreport.WriteJSON(path, r); err != nil {
		t.Fatal(err)
	}
	return path
}

// passingReport is a report that passed at commit, its oldest record begun
// at oldest.
func passingReport(t *testing.T, commit string, oldest time.Time) string {
	t.Helper()
	return reportFor(t, commit, flakegate.Stamp{Commit: commit, Clean: true}, commit, oldest)
}

// readReceipt reads the pr-check receipt a fixture holds.
func readReceipt(t *testing.T, dir string) Receipt {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, ".git", prCheckReceiptName))
	if err != nil {
		t.Fatalf("reading the pr-check receipt: %v", err)
	}
	var r Receipt
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("the receipt does not parse: %v", err)
	}
	return r
}

// prCheckArgs is write's arguments for a pr-check run at HEAD over report.
func prCheckArgs(t *testing.T, report string) []string {
	t.Helper()
	return []string{"--target", "pr-check", "--started-at", headOf(t), "--started-clean", "yes", "--report", report}
}

// TestWriteKeepsTheReportsOldestRecord: a pr-check receipt over a passing
// report of HEAD is written, and keeps when the run's oldest record began.
func TestWriteKeepsTheReportsOldestRecord(t *testing.T) {
	dir := newRepo(t)
	oldest := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	if err := runWrite(prCheckArgs(t, passingReport(t, headOf(t), oldest))); err != nil {
		t.Fatalf("a passing report of HEAD was refused: %v", err)
	}
	r := readReceipt(t, dir)
	if r.Commit != headOf(t) || r.Target != "pr-check" || r.Oldest == nil || !r.Oldest.Equal(oldest) {
		t.Errorf("receipt %+v, want HEAD with its oldest record at %s", r, oldest)
	}
}

// TestWriteRefusesAReportThatIsNotAPassOfHEAD: a report of another commit,
// one with a record kept from an earlier commit (stale), one run on a dirty
// tree (dirty), one missing a part, one that cannot be read, and one that
// says pass while listing a problem are each refused, no receipt written,
// the refusal saying why.
func TestWriteRefusesAReportThatIsNotAPassOfHEAD(t *testing.T) {
	dir := newRepo(t)
	older := headOf(t)
	head := addCommit(t, dir, "two")
	clean := flakegate.Stamp{Commit: head, Clean: true}

	missing := t.TempDir()
	if _, err := prreport.Begin(missing, head, []string{"fast", "win"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := flakegate.WriteRun(filepath.Join(missing, "fast.json"), flakegate.Run{Verdict: flakegate.VerdictPass, Commit: head, Clean: true}); err != nil {
		t.Fatal(err)
	}
	r, err := prreport.Merge(missing, prreport.MergeOptions{Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := prreport.WriteJSON(filepath.Join(missing, prreport.ReportFile), r); err != nil {
		t.Fatal(err)
	}

	edited := t.TempDir()
	body := `{"schema":2,"kind":"pr-check","commit":"` + head + `","verdict":"pass","generated":"2026-10-24T12:00:00Z",` +
		`"problems":[{"code":"stale","record":"fast.json","detail":"an earlier run's"}]}`
	if err := os.WriteFile(filepath.Join(edited, prreport.ReportFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	unnamed := t.TempDir()
	if err := os.WriteFile(filepath.Join(unnamed, prreport.ReportFile),
		[]byte(`{"schema":2,"kind":"pr-check","verdict":"pass","generated":"2026-10-24T12:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct{ report, want string }{
		"another commit's report": {passingReport(t, older, time.Now()), "is for " + short(older)},
		"a report of no commit":   {filepath.Join(unnamed, prreport.ReportFile), "is for no commit"},
		"a kept record (stale)":   {reportFor(t, head, clean, older, time.Now()), "stale: mac.json"},
		"a dirty tree":            {reportFor(t, head, flakegate.Stamp{Commit: head}, head, time.Now()), "dirty: fast.json"},
		"a missing part":          {filepath.Join(missing, prreport.ReportFile), "missing: win"},
		"no report":               {filepath.Join(t.TempDir(), prreport.ReportFile), "could not be read"},
		"a pass with a problem":   {filepath.Join(edited, prreport.ReportFile), "still lists 0 missing part(s) and 1 problem(s)"},
	} {
		err := runWrite(prCheckArgs(t, tc.report))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want it refused saying %q", name, err, tc.want)
		}
		if _, statErr := os.Stat(filepath.Join(dir, ".git", prCheckReceiptName)); !os.IsNotExist(statErr) {
			t.Fatalf("%s: a receipt was written despite the refusal", name)
		}
	}
}

// TestWriteNeedsTheReportOnlyForPRCheck: pr-check without a report is
// refused, naming the flag; the whole suite with one is refused too, since
// pr-check's report says nothing about it.
func TestWriteNeedsTheReportOnlyForPRCheck(t *testing.T) {
	newRepo(t)
	head := headOf(t)
	if err := runWrite([]string{"--target", "pr-check", "--started-at", head, "--started-clean", "yes"}); err == nil || !strings.Contains(err.Error(), "needs --report") {
		t.Errorf("pr-check with no report: %v", err)
	}
	report := passingReport(t, head, time.Now())
	if err := runWrite([]string{"--target", "ci", "--started-at", head, "--started-clean", "yes", "--report", report}); err == nil || !strings.Contains(err.Error(), "says nothing about the ci gate") {
		t.Errorf("ci with pr-check's report: %v", err)
	}
	// The existing refusals still come first: a dirty start is refused
	// before the report is read.
	if err := runWrite([]string{"--target", "pr-check", "--started-at", head, "--started-clean", "no", "--report", report}); err == nil || !strings.Contains(err.Error(), "already had uncommitted changes") {
		t.Errorf("a dirty start with a passing report: %v", err)
	}
}

// TestVerifyCountsTheAgeFromTheOldestRecord: a receipt written a minute
// ago over a record kept from two days ago is past the bound and refused,
// naming the kept record; one whose oldest record is within the bound
// passes; one whose oldest record is dated after it was written (a clock
// that stepped) is bounded by when it was written.
func TestVerifyCountsTheAgeFromTheOldestRecord(t *testing.T) {
	dir := newRepo(t)
	withRemoteMain(t, dir)
	tip := addCommit(t, dir, "two")
	written := time.Now().UTC().Add(-time.Minute)
	for name, tc := range map[string]struct {
		oldest time.Time
		want   string
	}{
		"kept from two days ago": {written.Add(-48 * time.Hour), "oldest record in its run began"},
		"kept from this morning": {written.Add(-3 * time.Hour), ""},
		"dated after writing":    {written.Add(time.Hour), ""},
	} {
		oldest := tc.oldest
		writeRawPRCheckReceipt(t, dir, Receipt{Commit: tip, Target: "pr-check", WrittenAt: written, Oldest: &oldest})
		pushLines(t, "refs/heads/topic "+tip+" refs/heads/topic "+zeros+"\n")
		err := runVerify([]string{"--push-stdin", "--remote", "origin"})
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: refused: %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "govulncheck")):
			t.Errorf("%s: %v, want it refused saying %q and why", name, err, tc.want)
		}
	}
}

// TestWriteThenVerifyAKeptRecordsAge is the whole path: a pass whose report
// kept a record from twenty-five hours ago writes a receipt, which verify
// then refuses, since that record's govulncheck answer is past the bound.
func TestWriteThenVerifyAKeptRecordsAge(t *testing.T) {
	dir := newRepo(t)
	withRemoteMain(t, dir)
	tip := addCommit(t, dir, "two")
	if err := runWrite(prCheckArgs(t, passingReport(t, tip, time.Now().Add(-25*time.Hour)))); err != nil {
		t.Fatalf("runWrite: %v", err)
	}
	pushLines(t, "refs/heads/topic "+tip+" refs/heads/topic "+zeros+"\n")
	if err := runVerify([]string{"--push-stdin", "--remote", "origin"}); err == nil || !strings.Contains(err.Error(), "make pr-check LEGS") {
		t.Errorf("a receipt over a record kept past the bound: %v", err)
	}
}

// writeRawPRCheckReceipt puts a pr-check receipt on disk without going
// through write.
func writeRawPRCheckReceipt(t *testing.T, dir string, r Receipt) {
	t.Helper()
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", prCheckReceiptName), body, 0o600); err != nil {
		t.Fatal(err)
	}
}
