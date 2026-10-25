// Tests of record-prcheck against a real pr-check report: one merged by
// tools/internal/prreport from records written by each producer's own
// writer, as make pr-check leaves it, then read back by this tool.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// testCommit is the commit the fixtures' runs checked.
const testCommit = "1fa9390d9e6736bec053983246a95a3b2b81ba39"

// testAt is when the fixtures' runs began.
var testAt = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// testStamp is what the fixtures' records carry: testCommit, on a clean
// tree.
var testStamp = flakegate.Stamp{Commit: testCommit, Clean: true}

// writeRecords writes a records directory as a pr-check run does: a fast
// pass with a real failure and a contention, a strict repeat with a failure,
// a Mac leg that could not reach its machine, a Windows leg whose repeat
// failed, a step that failed and one that could not run. The Mac's name and
// address are a user@host the ledger must never hold.
func writeRecords(t *testing.T, dir string) {
	t.Helper()
	if _, err := prreport.Begin(dir, testCommit, []string{"fast", "repeat", "mac", "win", "bsd", "coverage", "images"}, testAt); err != nil {
		t.Fatal(err)
	}
	full := "testgate -race -timeout 30m -tags integration -cover -count=1 -p 1"
	repeat := "testgate -count=3 -timeout 30m -p 1"
	runs := map[string]flakegate.Run{
		"fast": {Label: full, OS: "linux", Arch: "amd64", Packages: []string{modulePrefix + "x/a", modulePrefix + "x/b"}, Commit: testCommit, Clean: true, Started: testAt, Verdict: flakegate.VerdictFail, Failures: []flakegate.RunFailure{
			{Package: "x/a", Test: "TestBroken", Kind: "test", Outcome: flakegate.OutcomeFailed,
				Output: "=== RUN   TestBroken\n    a_test.go:9: dial someone@192.0.2.10 from /home/someone/src: got refused\n--- FAIL: TestBroken (0.01s)"},
			{Package: "x/a", Test: "TestRace", Kind: "test", Outcome: flakegate.OutcomeContention, Output: "--- FAIL: TestRace"},
			{Package: "x/b", Kind: "package", Outcome: flakegate.OutcomeNotRerun},
		}},
		"repeat": {Label: repeat, OS: "linux", Arch: "amd64", Packages: []string{modulePrefix + "x/a"}, Commit: testCommit, Clean: true, Started: testAt, Verdict: flakegate.VerdictFail, Failures: []flakegate.RunFailure{
			{Package: "x/a", Test: "TestFlaky", Kind: "test", Outcome: flakegate.OutcomeFailed},
		}},
	}
	for name, r := range runs {
		if err := flakegate.WriteRun(filepath.Join(dir, name+".json"), r); err != nil {
			t.Fatal(err)
		}
	}
	winRepeat := flakegate.Run{Schema: flakegate.RunSchema, Kind: flakegate.RunKind, Label: repeat, OS: "windows", Verdict: flakegate.VerdictFail,
		Failures: []flakegate.RunFailure{{Package: "x/t", Test: "TestQuiet", Kind: "test", Outcome: flakegate.OutcomeFailed}}}
	winFull := flakegate.Run{Schema: flakegate.RunSchema, Kind: flakegate.RunKind, Label: full, OS: "windows", Verdict: flakegate.VerdictPass,
		Failures: []flakegate.RunFailure{{Package: "x/t", Test: "TestTelnet", Kind: "test", Outcome: flakegate.OutcomeContention}}}
	legs := map[string]oscheck.LegRecord{
		"mac": {Tool: "maccheck", OS: "macOS", Target: "someone@192.0.2.10", Commit: testCommit, Started: testAt, Verdict: flakegate.VerdictNotRun,
			Step: oscheck.StepReachable, Error: "someone@192.0.2.10 (192.0.2.10:22) did not answer on its SSH port within 5s"},
		"win": {Tool: "wincheck", OS: "Windows", Target: "win-check", Commit: testCommit, Started: testAt, Verdict: flakegate.VerdictFail,
			Runs: []flakegate.Run{winFull, winRepeat}},
		"bsd": {Tool: "bsdcheck", OS: "FreeBSD", Target: "bsd-check", Commit: testCommit, Started: testAt, Verdict: flakegate.VerdictNotRun,
			Step: oscheck.StepSettings, Error: "no FreeBSD machine configured (tests/bsd/.env)"},
	}
	for name, l := range legs {
		if err := oscheck.WriteLegRecord(filepath.Join(dir, name+".json"), l); err != nil {
			t.Fatal(err)
		}
	}
	if err := prreport.WriteStep(dir, "coverage", 1, "", testStamp, testAt); err != nil {
		t.Fatal(err)
	}
	if err := prreport.WriteStep(dir, "images", 0, "no Docker daemon answered", testStamp, testAt); err != nil {
		t.Fatal(err)
	}
}

// mergedReport writes records and merges them into report.json as make
// pr-check's last step does, returning its path.
func mergedReport(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeRecords(t, dir)
	one := 1
	r, err := prreport.Merge(dir, prreport.MergeOptions{Commit: testCommit, ExitCode: &one, Now: testAt.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, prreport.ReportFile)
	if err := prreport.WriteJSON(path, r); err != nil {
		t.Fatal(err)
	}
	return path
}

// runTool runs the command and fails the test on a non-zero exit.
func runTool(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(args, &stdout, &stderr, nil); code != 0 {
		t.Fatalf("failures %s: exit %d: %s", strings.Join(args, " "), code, stderr.String())
	}
	return stdout.String()
}

// TestRecordPRCheck_FromARealReport: every failure, contention and leg that
// did not pass becomes an entry with the right verdict, a second recording
// adds nothing, and no machine's name or address reaches the file.
func TestRecordPRCheck_FromARealReport(t *testing.T) {
	report, dir := mergedReport(t), t.TempDir()
	runTool(t, "record-prcheck", "-dir", dir, "-report", report, "-branch", "feat/x")
	entries, err := readLedger(ledgerFile(dir, SourcePRCheck))
	if err != nil {
		t.Fatal(err)
	}
	want := []brief{
		{KindTest, "fast", "", "linux", "x/a", "TestBroken", VerdictReal, ""},
		{KindTest, "fast", "", "linux", "x/a", "TestRace", VerdictContention, ""},
		{KindTest, "fast", "", "linux", "x/b", "", VerdictNotRerun, "the package's test binary failed or timed out"},
		{KindTest, "repeat", "", "linux", "x/a", "TestFlaky", VerdictStrict, ""},
		{KindLeg, "mac", "", "macOS", "", "", VerdictNotRun, "stopped at reachable: <target> (<ip>:22) did not answer on its SSH port within 5s"},
		{KindLeg, "bsd", "", "FreeBSD", "", "", VerdictNotRun, "no FreeBSD machine configured (tests/bsd/.env)"},
		{KindTest, "win", "full", "Windows", "x/t", "TestTelnet", VerdictContention, ""},
		{KindTest, "win", "repeat", "Windows", "x/t", "TestQuiet", VerdictStrict, ""},
		{KindLeg, "images", "", "", "", "", VerdictNotRun, "no Docker daemon answered"},
		{KindLeg, "coverage", "", "", "", "", VerdictFail, "exit 1"},
	}
	if b := briefs(entries); !equalBriefs(b, want) {
		t.Errorf("\n got %+v\nwant %+v", b, want)
	}
	e := entries[0]
	if e.Commit != testCommit || e.Branch != "feat/x" || !e.SeenAt.Equal(testAt) || e.Run != "2026-10-24T12:00:00Z/"+testCommit || e.Schema != EntrySchema {
		t.Errorf("the run's details: %+v", e)
	}
	if e.Error != "a_test.go:9: dial <user@host> from ~/src: got refused" {
		t.Errorf("the error line is %q", e.Error)
	}
	if out := runTool(t, "record-prcheck", "-dir", dir, "-report", report, "-branch", "feat/x"); !strings.Contains(out, "0 of 10 entries new") {
		t.Errorf("a second recording said %q", out)
	}
	data, err := os.ReadFile(ledgerFile(dir, SourcePRCheck))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"someone", "192.0.2.10", "/home/", "win-check", "bsd-check"} {
		if strings.Contains(string(data), leak) {
			t.Errorf("the ledger holds %q:\n%s", leak, data)
		}
	}
}

// TestRecordPRCheck_ARunningReport: a report still saying running is not
// recorded unless the run is called interrupted, and then what its records
// hold is, with an entry saying it was interrupted.
func TestRecordPRCheck_ARunningReport(t *testing.T) {
	records, dir := t.TempDir(), t.TempDir()
	writeRecords(t, records)
	report := filepath.Join(records, prreport.ReportFile)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"record-prcheck", "-dir", dir, "-report", report}, &stdout, &stderr, nil); code != 1 || !strings.Contains(stderr.String(), "still says running") {
		t.Errorf("a running report: exit %d, %s", code, stderr.String())
	}
	runTool(t, "record-prcheck", "-dir", dir, "-report", report, "-interrupted", "-branch", "b")
	entries, err := readLedger(ledgerFile(dir, SourcePRCheck))
	if err != nil {
		t.Fatal(err)
	}
	last := entries[len(entries)-1]
	if last.Leg != "pr-check" || last.Verdict != VerdictNotRun || !strings.HasPrefix(last.Reason, "interrupted: ") {
		t.Errorf("the last entry is %+v", last)
	}
}

// TestRecordPRCheck_FromALog: a saved log is recorded for the commit its
// summary names, resolved to the full SHA, at -at when given.
func TestRecordPRCheck_FromALog(t *testing.T) {
	resolved := ""
	defer func(old func(string) (string, error)) { resolveCommit = old }(resolveCommit)
	resolveCommit = func(c string) (string, error) { resolved = c; return "d1e221fa8162ce99d01689c5cc0db70608a03803", nil }
	dir := t.TempDir()
	runTool(t, "record-prcheck", "-dir", dir, "-log", filepath.Join("testdata", "prcheck-summary-15.log"), "-at", "2026-10-24T05:01:00Z", "-branch", "b")
	entries, err := readLedger(ledgerFile(dir, SourcePRCheck))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "d1e221fa8162" || len(entries) != 2 || entries[0].Commit != "d1e221fa8162ce99d01689c5cc0db70608a03803" ||
		entries[0].Run != "2026-10-24T05:01:00Z/d1e221fa8162ce99d01689c5cc0db70608a03803" {
		t.Errorf("resolved %q, entries %+v", resolved, entries)
	}
	var stderr bytes.Buffer
	if code := run([]string{"record-prcheck", "-dir", dir, "-log", filepath.Join("testdata", "prcheck-summary-15.log"), "-at", "yesterday"}, &bytes.Buffer{}, &stderr, nil); code != 2 {
		t.Errorf("a bad -at: exit %d, %s", code, stderr.String())
	}
}

// TestFromReport_KeptRecordsAreNotCountedAgain: a pass, leg and step that
// began before the run (kept by make pr-check LEGS=..., and recorded by the
// run that wrote them) add no entry, while this run's own do.
func TestFromReport_KeptRecordsAreNotCountedAgain(t *testing.T) {
	started := testAt
	earlier := testAt.Add(-2 * time.Hour)
	failed := flakegate.Run{OS: "linux", Started: earlier, Verdict: flakegate.VerdictFail,
		Failures: []flakegate.RunFailure{{Package: "x/a", Test: "TestOld", Kind: "test", Outcome: flakegate.OutcomeFailed}}}
	fresh := failed
	fresh.Started = testAt.Add(time.Minute)
	fresh.Failures = []flakegate.RunFailure{{Package: "x/a", Test: "TestNew", Kind: "test", Outcome: flakegate.OutcomeFailed}}
	r := prreport.Report{Commit: testCommit, Started: &started, Generated: testAt.Add(time.Hour),
		Passes: []prreport.Pass{{Name: "fast", Run: failed}, {Name: "repeat", Run: fresh}},
		Legs:   []prreport.Leg{{Name: "mac", Leg: oscheck.LegRecord{OS: "macOS", Started: earlier, Verdict: flakegate.VerdictNotRun, Step: oscheck.StepReachable}}},
		Steps:  []prreport.Step{{Name: "coverage", Verdict: flakegate.VerdictFail, ExitCode: 1, At: earlier}, {Name: "sizes", Verdict: flakegate.VerdictFail, ExitCode: 2, At: testAt.Add(time.Minute)}},
	}
	want := []brief{
		{KindTest, "repeat", "", "linux", "x/a", "TestNew", VerdictReal, ""},
		{KindLeg, "sizes", "", "", "", "", VerdictFail, "exit 2"},
	}
	if b := briefs(fromReport(r, "b", testAt)); !equalBriefs(b, want) {
		t.Errorf("\n got %+v\nwant %+v", b, want)
	}
}

// TestFromReport_PassesStepsAndProblems: a pass that reached no verdict, a
// leg that failed with no test to blame, a part with no record and a
// problem each become a leg entry, and a build failure keeps its words.
func TestFromReport_PassesStepsAndProblems(t *testing.T) {
	r := prreport.Report{Schema: prreport.ReportSchema, Kind: prreport.ReportKind, Commit: testCommit, Generated: testAt,
		Passes: []prreport.Pass{
			{Name: "containers", Run: flakegate.Run{OS: "linux", Verdict: flakegate.VerdictError, Error: "go test would not start"}},
			{Name: "fast", Run: flakegate.Run{OS: "linux", Verdict: flakegate.VerdictFail, Failures: []flakegate.RunFailure{{Package: "x/a", Kind: "build", Outcome: flakegate.OutcomeFailed}}}},
		},
		Legs:     []prreport.Leg{{Name: "bsd", Leg: oscheck.LegRecord{OS: "FreeBSD", Target: "box", Verdict: flakegate.VerdictFail, Error: "the box could not be put back"}}},
		Missing:  []string{"win"},
		Problems: []prreport.Problem{{Code: prreport.ProblemMissing, Record: "win"}, {Code: prreport.ProblemGateFailed, Detail: "pr-check exited 2"}},
	}
	want := []brief{
		{KindLeg, "containers", "", "", "", "", VerdictFail, "go test would not start"},
		{KindTest, "fast", "", "linux", "x/a", "", VerdictReal, "build failed"},
		{KindLeg, "bsd", "", "FreeBSD", "", "", VerdictFail, "the <target> could not be put back"},
		{KindLeg, "win", "", "", "", "", VerdictNotRun, "no record of it: pr-check said it would run it"},
		{KindLeg, "pr-check", "", "", "", "", VerdictFail, "gate-failed: pr-check exited 2"},
	}
	got := fromReport(r, "b", testAt)
	if b := briefs(got); !equalBriefs(b, want) {
		t.Errorf("\n got %+v\nwant %+v", b, want)
	}
	if got[0].Run != "2026-10-24T12:00:00Z/"+testCommit {
		t.Errorf("a report with no start is run %q", got[0].Run)
	}
	if testVerdict("lost", false) != "lost" || packageReason("odd") != "odd" || legVerdict(flakegate.VerdictNotRun) != VerdictNotRun {
		t.Error("an unknown value was not kept as it is")
	}
}
