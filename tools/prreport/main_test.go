// Tests for the prreport command: begin, step and merge writing a report
// through tools/internal/prreport (whose own tests hold the contract), and
// that report read back as text, as JSON and in parts.
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/prreport"
)

// passing is a testgate record with nothing against it.
func passing(label string) flakegate.Run {
	return flakegate.Run{Label: label, OS: "linux", Packages: []string{"x/a"}, Verdict: flakegate.VerdictPass}
}

// writeRecords writes each record into dir by its name, a Run, a LegRecord
// or raw text.
func writeRecords(t *testing.T, dir string, records map[string]any) {
	t.Helper()
	for name, rec := range records {
		path := filepath.Join(dir, name)
		var err error
		switch rec := rec.(type) {
		case flakegate.Run:
			err = flakegate.WriteRun(path, rec)
		case oscheck.LegRecord:
			err = oscheck.WriteLegRecord(path, rec)
		case string:
			err = os.WriteFile(path, []byte(rec), 0o600)
		default:
			t.Fatalf("%s: a record of type %T", name, rec)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// fixture is a records directory as a failed pr-check leaves one: a fast
// pass with contention and a skip, a containers pass with a failed test and
// a build failure, a Mac leg that passed, a FreeBSD check with no machine,
// a Windows leg that never reached its tests, two steps, a stale report and
// a file that is not a record.
func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fast := passing("-full")
	fast.Skips = []flakegate.Skip{{Package: "x/a", Test: "TestNeedsDocker", Reason: "needs docker: no daemon"}}
	fast.Failures = []flakegate.RunFailure{{Package: "x/a", Test: "TestRace", Kind: "test", Outcome: flakegate.OutcomeContention, Output: "lost a race"}}
	containers := passing("-full -p 1")
	containers.Verdict = flakegate.VerdictFail
	containers.Failures = []flakegate.RunFailure{
		{Package: "x/b", Test: "TestBroken", Kind: "test", Outcome: flakegate.OutcomeFailed, Output: "want 1\ngot 2\n"},
		{Package: "x/c", Kind: "build", Outcome: flakegate.OutcomeFailed, Output: "undefined: y"},
	}
	repeat := passing("testgate -count=3 -timeout 30m -p 4")
	writeRecords(t, dir, map[string]any{
		"containers.json": containers,
		"fast.json":       fast,
		"win.json":        oscheck.LegRecord{Tool: "wincheck", OS: "Windows", Target: "win-check", Verdict: flakegate.VerdictNotRun, Step: oscheck.StepReachable, Error: "no route to host\nmore"},
		"bsd.json":        oscheck.LegRecord{Tool: "bsdcheck", OS: "FreeBSD", Verdict: flakegate.VerdictNotRun, Step: oscheck.StepSettings, Error: "tests/bsd/.env names no machine"},
		"mac.json":        oscheck.LegRecord{Tool: "maccheck", OS: "macOS", Target: "mac", Packages: []string{"x/a"}, Verdict: flakegate.VerdictPass, Runs: []flakegate.Run{passing("testgate -race -timeout 30m -tags integration -cover -count=1"), repeat}},
		"notes.txt":       "not a record",
	})
	if err := prreport.WriteStep(dir, "coverage", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := prreport.WriteStep(dir, "images", 0, "no Docker daemon answered"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// setHead makes HEAD commit for the test.
func setHead(t *testing.T, commit string) {
	old := headCommit
	headCommit = func() string { return commit }
	t.Cleanup(func() { headCommit = old })
}

// runCLI runs the command, returning its exit code and output.
func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestRun_MergeThenRead: merge writes the report and prints it, and the
// report reads back the same, whole, as JSON, by one entry, its failures
// alone and its skips.
func TestRun_MergeThenRead(t *testing.T) {
	setHead(t, "abc")
	dir := fixture(t)
	code, merged, stderr := runCLI("merge", "-dir", dir, "-commit", "abc", "-exit", "1")
	if code != 0 || stderr != "" {
		t.Fatalf("merge: %d %s", code, stderr)
	}
	for _, want := range []string{
		"at abc", "FAIL (exit 1)\n",
		"fast        pass     1 package(s), 0s, 0 failed, 1 contention, 1 skipped",
		"containers  fail     1 package(s), 0s, 2 failed",
		"mac         pass     macOS on mac, 1 package(s), 0s; full pass; repeat pass",
		"bsd         unset    FreeBSD: tests/bsd/.env names no machine",
		"win         not-run  Windows on win-check: stopped at reachable: no route to host\n",
		"images      not-run  no Docker daemon answered\n  coverage    pass     exit 0\n",
		"containers: x/b TestBroken [failed]\n      want 1\n      got 2\n",
		"containers: x/c (the package: build) [failed]\n      undefined: y",
		"Passed when re-run alone (1;", "fast: x/a TestRace",
		"1 test(s) skipped",
	} {
		if !strings.Contains(merged, want) {
			t.Errorf("the summary lacks %q:\n%s", want, merged)
		}
	}
	if code, shown, _ := runCLI("-dir", dir); code != 0 || shown != merged {
		t.Errorf("read back: %d\n%s", code, shown)
	}

	_, out, _ := runCLI("-dir", dir, "-json")
	var r prreport.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Verdict != flakegate.VerdictFail || len(r.Passes) != 2 || len(r.Legs) != 3 || *r.ExitCode != 1 {
		t.Errorf("-json: %v %+v", err, r)
	}
	_, out, _ = runCLI("-dir", dir, "-json", "-leg", "mac")
	r = prreport.Report{}
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Legs) != 1 || len(r.Passes) != 0 || r.Verdict != flakegate.VerdictPass || len(r.Legs[0].Leg.Runs) != 2 {
		t.Errorf("-leg mac: %v %+v", err, r)
	}
	for name, want := range map[string]string{"fast": flakegate.VerdictPass, "bsd": "unset", "images": flakegate.VerdictNotRun} {
		_, out, _ = runCLI("-dir", dir, "-json", "-leg", name)
		r = prreport.Report{}
		if err := json.Unmarshal([]byte(out), &r); err != nil || r.Verdict != want {
			t.Errorf("-leg %s: %v %+v", name, err, r)
		}
	}
	if code, _, stderr := runCLI("-dir", dir, "-leg", "linux"); code != 2 || !strings.Contains(stderr, `"linux"`) {
		t.Errorf("an entry the report lacks: %d %s", code, stderr)
	}

	_, out, _ = runCLI("-dir", dir, "-failures")
	if !strings.Contains(out, "TestBroken") || strings.Contains(out, "TestRace") || strings.Contains(out, "mac ") {
		t.Errorf("-failures:\n%s", out)
	}
	_, out, _ = runCLI("-dir", dir, "-skips")
	if !strings.Contains(out, "needs docker: no daemon") || !strings.Contains(out, "fast: x/a") {
		t.Errorf("-skips:\n%s", out)
	}
	green := t.TempDir()
	writeRecords(t, green, map[string]any{"fast.json": passing("-full")})
	runCLI("merge", "-dir", green)
	if _, out, _ = runCLI("-dir", green, "-skips"); out != "No test was skipped.\n" {
		t.Errorf("no skips: %q", out)
	}
}

// TestRun_AReportForAnotherCommitSaysSo: reading a report a commit other
// than HEAD left warns, naming both.
func TestRun_AReportForAnotherCommitSaysSo(t *testing.T) {
	setHead(t, "def")
	dir := fixture(t)
	runCLI("merge", "-dir", dir, "-commit", "abc")
	if code, _, stderr := runCLI("-dir", dir); code != 0 || !strings.Contains(stderr, "for abc, and HEAD is def") {
		t.Errorf("%d %q", code, stderr)
	}
}

// TestRun_Refusals: no report, a report of another kind, a directory that
// does not exist, an exit status that is not a number and a bad step name
// each exit 2 saying why.
func TestRun_Refusals(t *testing.T) {
	empty := t.TempDir()
	// scratch takes the cases that would write, were they not refused.
	scratch := t.TempDir()
	other := t.TempDir()
	writeRecords(t, other, map[string]any{"report.json": `{"schema":1,"kind":"leg"}`})
	newer := t.TempDir()
	writeRecords(t, newer, map[string]any{"report.json": `{"schema":3,"kind":"pr-check"}`})
	broken := t.TempDir()
	writeRecords(t, broken, map[string]any{"report.json": `{`})
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no report":             {[]string{"-dir", empty}, "make pr-check writes one"},
		"another kind":          {[]string{"-dir", other}, "not a pr-check report"},
		"a newer major":         {[]string{"-dir", newer}, "schema 3, where this reader knows 2"},
		"a bad expected name":   {[]string{"begin", "-dir", scratch, "-expect", "fast,../x"}, "not a record name"},
		"a stray to begin":      {[]string{"begin", "-dir", scratch, "now"}, `unexpected argument "now"`},
		"an unknown begin flag": {[]string{"begin", "-colour"}, "flag provided but not defined"},
		"not JSON":              {[]string{"-dir", broken}, "reading"},
		"no directory":          {[]string{"merge", "-dir", filepath.Join(empty, "absent")}, "reading"},
		"an exit not a number":  {[]string{"merge", "-dir", empty, "-exit", "one"}, "invalid value"},
		"a bad step name":       {[]string{"step", "-dir", empty, "-name", "../x"}, "not a step name"},
		"an unknown flag":       {[]string{"-colour"}, "flag provided but not defined"},
		"two views":             {[]string{"-dir", empty, "-failures", "-skips"}, "pass one of -json, -failures and -skips"},
		"a stray argument":      {[]string{"-dir", empty, "mac"}, `unexpected argument "mac"`},
		"a stray to merge":      {[]string{"merge", "-dir", scratch, "now"}, `unexpected argument "now"`},
		"a stray to step":       {[]string{"step", "-dir", scratch, "-name", "x", "1"}, `unexpected argument "1"`},
		"an exit not run":       {[]string{"step", "-dir", scratch, "-name", "x", "-exit", "1", "-not-run", "no daemon"}, "-exit or -not-run, not both"},
	} {
		if code, _, stderr := runCLI(tc.args...); code != 2 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: %d %q", name, code, stderr)
		}
	}
	if code, _, stderr := runCLI("step", "-dir", empty, "-name", "coverage", "-exit", "1"); code != 0 || stderr != "" {
		t.Fatalf("step: %d %s", code, stderr)
	}
	if r, _ := prreport.Merge(empty, prreport.MergeOptions{}); len(r.Steps) != 1 || r.Steps[0].Verdict != flakegate.VerdictFail {
		t.Errorf("the step's record: %+v", r)
	}
}

// TestRun_BeginThenAnInterruptedMerge: begin's report reads back as a run
// that has not ended, naming what it expects; the merge an interrupt makes
// says interrupted, lists what is missing, and says why; -leg on a missing
// part answers incomplete rather than failing to find it.
func TestRun_BeginThenAnInterruptedMerge(t *testing.T) {
	setHead(t, "abc")
	dir := t.TempDir()
	if code, _, stderr := runCLI("begin", "-dir", dir, "-commit", "abc", "-expect", "fast, repeat,win"); code != 0 || stderr != "" {
		t.Fatalf("begin: %d %s", code, stderr)
	}
	if code, out, _ := runCLI("-dir", dir); code != 0 || !strings.Contains(out, "RUNNING") ||
		!strings.Contains(out, "this is not a result. Expected: fast, repeat, win") {
		t.Errorf("a run that has not ended: %d\n%s", code, out)
	}
	writeRecords(t, dir, map[string]any{"fast.json": passing("-full")})
	code, out, stderr := runCLI("merge", "-dir", dir, "-interrupted", "-exit", "130")
	if code != 0 || stderr != "" {
		t.Fatalf("merge: %d %s", code, stderr)
	}
	for _, want := range []string{"at abc", "INTERRUPTED (exit 130)", "repeat      missing  no record of it", "win         missing  no record of it",
		"Problem (interrupted) pr-check stopped before it finished"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Problem (missing)") {
		t.Errorf("a missing part was said twice:\n%s", out)
	}
	_, out, _ = runCLI("-dir", dir, "-json", "-leg", "win")
	var r prreport.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Verdict != prreport.VerdictIncomplete || len(r.Missing) != 1 ||
		len(r.Problems) != 1 || r.Problems[0].Code != prreport.ProblemMissing {
		t.Errorf("-leg on a missing part: %v %+v", err, r)
	}
}

// TestRun_ProblemsArePrintedWithTheirCodes: a record this reader cannot
// trust is printed with its code and file, and -leg carries the problems
// about that one record.
func TestRun_ProblemsArePrintedWithTheirCodes(t *testing.T) {
	setHead(t, "")
	dir := t.TempDir()
	flaky := passing("-full")
	flaky.Verdict = "quarantined"
	writeRecords(t, dir, map[string]any{"fast.json": flaky, "junk.json": "not json"})
	_, out, _ := runCLI("merge", "-dir", dir)
	for _, want := range []string{"INCOMPLETE", "fast        quarantined", "Problem (unknown-value) fast.json: the verdict \"quarantined\"", "Problem (unreadable) junk.json:"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary lacks %q:\n%s", want, out)
		}
	}
	_, out, _ = runCLI("-dir", dir, "-json", "-leg", "fast")
	var r prreport.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Problems) != 1 || r.Problems[0].Record != "fast.json" {
		t.Errorf("-leg fast: %v %+v", err, r)
	}
}
