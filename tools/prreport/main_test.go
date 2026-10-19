// Tests for prreport: a records directory merged into one report with the
// verdict pr-check's own status allows, and that report read back as text,
// as JSON and in parts.
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
		"report.json":     "a stale report",
		"notes.txt":       "not a record",
	})
	if err := writeStep(dir, "coverage", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := writeStep(dir, "images", 0, "no Docker daemon answered"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// names is each entry's name, in order.
func names[T any](entries []T, name func(T) string) []string {
	var out []string
	for _, e := range entries {
		out = append(out, name(e))
	}
	return out
}

// TestMerge_ReadsEveryRecordInOrder: every record but the report is read by
// its kind, passes and legs in the order pr-check runs them.
func TestMerge_ReadsEveryRecordInOrder(t *testing.T) {
	r, err := merge(fixture(t), "abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(r.Passes, func(p Pass) string { return p.Name }), " "); got != "fast containers" {
		t.Errorf("passes %q", got)
	}
	if got := strings.Join(names(r.Legs, func(l Leg) string { return l.Name }), " "); got != "mac bsd win" {
		t.Errorf("legs %q", got)
	}
	if len(r.Steps) != 2 || len(r.Problems) != 0 || r.Commit != "abc" || r.Kind != ReportKind || r.Schema != ReportSchema {
		t.Errorf("report %+v", r)
	}
	if r.Verdict != flakegate.VerdictFail {
		t.Errorf("verdict %s", r.Verdict)
	}
	if _, err := merge(filepath.Join(t.TempDir(), "absent"), "", nil); err == nil {
		t.Error("a directory that does not exist was merged")
	}
}

// TestVerdict: anything that failed, could not finish or did not run fails
// the report, but for a check with no machine and a step that could not run
// here; a record that could not be read leaves it incomplete.
func TestVerdict(t *testing.T) {
	pass := Pass{Name: "fast", Run: passing("-full")}
	failed := Pass{Name: "fast", Run: flakegate.Run{Verdict: flakegate.VerdictFail}}
	unset := Leg{Name: "bsd", Leg: oscheck.LegRecord{Verdict: flakegate.VerdictNotRun, Step: oscheck.StepSettings}}
	unreachable := Leg{Name: "win", Leg: oscheck.LegRecord{Verdict: flakegate.VerdictNotRun, Step: oscheck.StepReachable}}
	errored := Leg{Name: "mac", Leg: oscheck.LegRecord{Verdict: flakegate.VerdictError, Step: oscheck.StepTest}}
	for name, tc := range map[string]struct {
		r    Report
		want string
	}{
		"nothing against it":             {Report{Passes: []Pass{pass}}, flakegate.VerdictPass},
		"a check with no machine":        {Report{Passes: []Pass{pass}, Legs: []Leg{unset}}, flakegate.VerdictPass},
		"a step that could not":          {Report{Steps: []Step{{Verdict: flakegate.VerdictNotRun}}}, flakegate.VerdictPass},
		"a failed pass":                  {Report{Passes: []Pass{failed}}, flakegate.VerdictFail},
		"an unreachable machine":         {Report{Legs: []Leg{unreachable}}, flakegate.VerdictFail},
		"tests of unknown outcome":       {Report{Legs: []Leg{errored}}, flakegate.VerdictFail},
		"a failed step":                  {Report{Steps: []Step{{Verdict: flakegate.VerdictFail}}}, flakegate.VerdictFail},
		"an unread record":               {Report{Passes: []Pass{pass}, Problems: []string{"x"}}, VerdictIncomplete},
		"an unread record and a failure": {Report{Passes: []Pass{failed}, Problems: []string{"x"}}, flakegate.VerdictFail},
	} {
		if got := tc.r.verdict(); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
}

// TestMerge_TheGatesStatusDecides: a report never passes a run pr-check
// failed, and a record that failed in a run pr-check passed is said to
// disagree with it.
func TestMerge_TheGatesStatusDecides(t *testing.T) {
	one, zero := 1, 0
	green := t.TempDir()
	writeRecords(t, green, map[string]any{"fast.json": passing("-full")})
	r, err := merge(green, "", &one)
	if err != nil || r.Verdict != flakegate.VerdictFail || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "exited 1") {
		t.Errorf("a gate that failed with every record passing: %v %+v", err, r)
	}
	if r, _ := merge(green, "", &zero); r.Verdict != flakegate.VerdictPass || len(r.Problems) != 0 || *r.ExitCode != 0 {
		t.Errorf("a gate that passed: %+v", r)
	}
	if r, _ := merge(fixture(t), "", &zero); r.Verdict != flakegate.VerdictFail || len(r.Problems) != 1 || !strings.Contains(r.Problems[0], "one of the two is wrong") {
		t.Errorf("a gate that passed over a failed record: %+v", r)
	}
	if r, _ := merge(fixture(t), "", &one); len(r.Problems) != 0 {
		t.Errorf("a gate that failed with a record saying why: %+v", r.Problems)
	}
}

// TestMerge_AnUnreadableRecordIsAProblem: a file that is not a record, a
// kind this reader does not know and a schema it does not know are each
// named, and leave a run with nothing against it incomplete.
func TestMerge_AnUnreadableRecordIsAProblem(t *testing.T) {
	dir := t.TempDir()
	writeRecords(t, dir, map[string]any{
		"fast.json":   passing("-full"),
		"junk.json":   "go test: no such file",
		"other.json":  `{"schema":1,"kind":"lint"}`,
		"future.json": `{"schema":2,"kind":"testgate","verdict":"pass"}`,
		"leg.json":    `{"schema":2,"kind":"leg"}`,
		"step.json":   `{"schema":2,"kind":"step"}`,
	})
	r, err := merge(dir, "", nil)
	if err != nil || r.Verdict != VerdictIncomplete || len(r.Problems) != 5 {
		t.Fatalf("%v %s %q", err, r.Verdict, r.Problems)
	}
	for _, name := range []string{"junk.json", "other.json", "future.json", "leg.json", "step.json"} {
		if !strings.Contains(strings.Join(r.Problems, "\n"), name+":") {
			t.Errorf("%s is not named among %q", name, r.Problems)
		}
	}
}

// TestWriteStep: a step is recorded by its exit code, or as not run with
// why; a name that would not be its own file in the directory is refused.
func TestWriteStep(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		exit    int
		notRun  string
		verdict string
	}{
		"passed":  {0, "", flakegate.VerdictPass},
		"failed":  {3, "", flakegate.VerdictFail},
		"skipped": {0, "no daemon", flakegate.VerdictNotRun},
	} {
		if err := writeStep(dir, name, tc.exit, tc.notRun); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var s Step
		if err := json.Unmarshal(data, &s); err != nil || s.Verdict != tc.verdict || s.ExitCode != tc.exit || s.Reason != tc.notRun || s.Kind != StepKind || s.Name != name {
			t.Errorf("%s: %v %+v", name, err, s)
		}
	}
	for _, bad := range []string{"", "report", "../x", "a.b", "a/b", "a b"} {
		if err := writeStep(dir, bad, 0, ""); err == nil {
			t.Errorf("the step name %q was taken", bad)
		}
	}
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
	var r Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Verdict != flakegate.VerdictFail || len(r.Passes) != 2 || len(r.Legs) != 3 || *r.ExitCode != 1 {
		t.Errorf("-json: %v %+v", err, r)
	}
	_, out, _ = runCLI("-dir", dir, "-json", "-leg", "mac")
	r = Report{}
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Legs) != 1 || len(r.Passes) != 0 || r.Verdict != flakegate.VerdictPass || len(r.Legs[0].Leg.Runs) != 2 {
		t.Errorf("-leg mac: %v %+v", err, r)
	}
	for name, want := range map[string]string{"fast": flakegate.VerdictPass, "bsd": "unset", "images": flakegate.VerdictNotRun} {
		_, out, _ = runCLI("-dir", dir, "-json", "-leg", name)
		r = Report{}
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
	broken := t.TempDir()
	writeRecords(t, broken, map[string]any{"report.json": `{`})
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no report":            {[]string{"-dir", empty}, "make pr-check writes one"},
		"another kind":         {[]string{"-dir", other}, "not a pr-check report"},
		"not JSON":             {[]string{"-dir", broken}, "reading"},
		"no directory":         {[]string{"merge", "-dir", filepath.Join(empty, "absent")}, "reading"},
		"an exit not a number": {[]string{"merge", "-dir", empty, "-exit", "one"}, "invalid value"},
		"a bad step name":      {[]string{"step", "-dir", empty, "-name", "../x"}, "not a step name"},
		"an unknown flag":      {[]string{"-colour"}, "flag provided but not defined"},
		"two views":            {[]string{"-dir", empty, "-failures", "-skips"}, "pass one of -json, -failures and -skips"},
		"a stray argument":     {[]string{"-dir", empty, "mac"}, `unexpected argument "mac"`},
		"a stray to merge":     {[]string{"merge", "-dir", scratch, "now"}, `unexpected argument "now"`},
		"a stray to step":      {[]string{"step", "-dir", scratch, "-name", "x", "1"}, `unexpected argument "1"`},
		"an exit not run":      {[]string{"step", "-dir", scratch, "-name", "x", "-exit", "1", "-not-run", "no daemon"}, "-exit or -not-run, not both"},
	} {
		if code, _, stderr := runCLI(tc.args...); code != 2 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: %d %q", name, code, stderr)
		}
	}
	if code, _, stderr := runCLI("step", "-dir", empty, "-name", "coverage", "-exit", "1"); code != 0 || stderr != "" {
		t.Fatalf("step: %d %s", code, stderr)
	}
	if r, _ := merge(empty, "", nil); len(r.Steps) != 1 || r.Steps[0].Verdict != flakegate.VerdictFail {
		t.Errorf("the step's record: %+v", r)
	}
}
