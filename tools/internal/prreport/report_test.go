// Tests for the report as a contract: a records directory merged into one
// report with the verdict its records, its expectations and pr-check's own
// status allow, and what a reader does with records and reports it cannot
// simply trust: missing legs, malformed records, interrupted runs, values
// it does not know, and reports newer or other than it knows.
package prreport

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// passing is a testgate record with nothing against it.
func passing(label string) flakegate.Run {
	return flakegate.Run{Label: label, OS: "linux", Packages: []string{"x/a"}, Verdict: flakegate.VerdictPass}
}

// writeRecords writes each record into dir by its name: a Run, a LegRecord,
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

// merge merges dir with o, failing the test on an error.
func merge(t *testing.T, dir string, o MergeOptions) Report {
	t.Helper()
	if o.Now.IsZero() {
		o.Now = now
	}
	r, err := Merge(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// codes is each problem's code, in order.
func codes(r Report) []string {
	var out []string
	for _, p := range r.Problems {
		out = append(out, p.Code)
	}
	return out
}

// fixture is a records directory as a failed pr-check leaves one, written
// through the producers' own writers: a fast pass with contention and a
// skip, a containers pass with a failed test and a build failure, a Mac leg
// that passed, a FreeBSD check with no machine, a Windows leg that never
// reached its tests, two steps, and a file that is not JSON at all.
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
	writeRecords(t, dir, map[string]any{
		"containers.json": containers,
		"fast.json":       fast,
		"win.json":        oscheck.LegRecord{Tool: "wincheck", OS: "Windows", Target: "win-check", Verdict: flakegate.VerdictNotRun, Step: oscheck.StepReachable, Error: "no route to host"},
		"bsd.json":        oscheck.LegRecord{Tool: "bsdcheck", OS: "FreeBSD", Verdict: flakegate.VerdictNotRun, Step: oscheck.StepSettings, Error: "tests/bsd/.env names no machine"},
		"mac.json":        oscheck.LegRecord{Tool: "maccheck", OS: "macOS", Target: "mac", Packages: []string{"x/a"}, Verdict: flakegate.VerdictPass, Runs: []flakegate.Run{passing("-full"), passing("-count=3")}},
		"notes.txt":       "not a record",
	})
	if err := WriteStep(dir, "coverage", 0, ""); err != nil {
		t.Fatal(err)
	}
	if err := WriteStep(dir, "images", 0, "no Docker daemon answered"); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestMerge_ReadsEveryRecordInOrder: every record but the report is read by
// its kind, each list in the order pr-check runs them.
func TestMerge_ReadsEveryRecordInOrder(t *testing.T) {
	r := merge(t, fixture(t), MergeOptions{Commit: "abc"})
	var passes, legs, steps []string
	for _, p := range r.Passes {
		passes = append(passes, p.Name)
	}
	for _, l := range r.Legs {
		legs = append(legs, l.Name)
	}
	for _, s := range r.Steps {
		steps = append(steps, s.Name)
	}
	if !slices.Equal(passes, []string{"fast", "containers"}) || !slices.Equal(legs, []string{"mac", "bsd", "win"}) || !slices.Equal(steps, []string{"images", "coverage"}) {
		t.Errorf("passes %v legs %v steps %v", passes, legs, steps)
	}
	if r.Verdict != flakegate.VerdictFail || len(r.Problems) != 0 || r.Commit != "abc" || r.Kind != ReportKind || r.Schema != ReportSchema || !r.Generated.Equal(now) {
		t.Errorf("report %+v", r)
	}
	if _, err := Merge(filepath.Join(t.TempDir(), "absent"), MergeOptions{}); err == nil {
		t.Error("a directory that does not exist was merged")
	}
}

// TestVerdict: anything that failed, could not finish or did not run fails
// the report, but for a check with no machine and a step that could not run
// here; a problem leaves an otherwise clean run incomplete; an interrupted
// run is that, whatever else holds.
func TestVerdict(t *testing.T) {
	pass := Pass{Name: "fast", Run: passing("-full")}
	failed := Pass{Name: "fast", Run: flakegate.Run{Verdict: flakegate.VerdictFail}}
	unset := Leg{Name: "bsd", Leg: oscheck.LegRecord{Verdict: flakegate.VerdictNotRun, Step: oscheck.StepSettings}}
	unreachable := Leg{Name: "win", Leg: oscheck.LegRecord{Verdict: flakegate.VerdictNotRun, Step: oscheck.StepReachable}}
	errored := Leg{Name: "mac", Leg: oscheck.LegRecord{Verdict: flakegate.VerdictError, Step: oscheck.StepTest}}
	problem := []Problem{{Code: ProblemUnreadable}}
	for name, tc := range map[string]struct {
		r           Report
		interrupted bool
		want        string
	}{
		"nothing against it":         {Report{Passes: []Pass{pass}}, false, flakegate.VerdictPass},
		"a check with no machine":    {Report{Passes: []Pass{pass}, Legs: []Leg{unset}}, false, flakegate.VerdictPass},
		"a step that could not":      {Report{Steps: []Step{{Verdict: flakegate.VerdictNotRun}}}, false, flakegate.VerdictPass},
		"a failed pass":              {Report{Passes: []Pass{failed}}, false, flakegate.VerdictFail},
		"an unreachable machine":     {Report{Legs: []Leg{unreachable}}, false, flakegate.VerdictFail},
		"tests of unknown outcome":   {Report{Legs: []Leg{errored}}, false, flakegate.VerdictFail},
		"a failed step":              {Report{Steps: []Step{{Verdict: flakegate.VerdictFail}}}, false, flakegate.VerdictFail},
		"a problem":                  {Report{Passes: []Pass{pass}, Problems: problem}, false, VerdictIncomplete},
		"a problem and a failure":    {Report{Passes: []Pass{failed}, Problems: problem}, false, flakegate.VerdictFail},
		"interrupted":                {Report{Passes: []Pass{pass}}, true, VerdictInterrupted},
		"interrupted after failing":  {Report{Passes: []Pass{failed}}, true, VerdictInterrupted},
		"a verdict of no known kind": {Report{Passes: []Pass{{Run: flakegate.Run{Verdict: "flaky"}}}}, false, flakegate.VerdictPass},
	} {
		if got := tc.r.verdict(tc.interrupted); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
	// The last case passes verdict() alone; a merge reaching it records the
	// unknown value as a problem, which is what makes it incomplete
	// (TestMerge_AValueThisReaderDoesNotKnow).
}

// TestMerge_TheGatesStatusDecides: a report never passes a run pr-check
// failed, and one that failed in a run pr-check passed says the two
// disagree.
func TestMerge_TheGatesStatusDecides(t *testing.T) {
	one, zero := 1, 0
	green := t.TempDir()
	writeRecords(t, green, map[string]any{"fast.json": passing("-full")})
	if r := merge(t, green, MergeOptions{ExitCode: &one}); r.Verdict != flakegate.VerdictFail || !slices.Equal(codes(r), []string{ProblemGateFailed}) {
		t.Errorf("a gate that failed with every record passing: %+v", r)
	}
	if r := merge(t, green, MergeOptions{ExitCode: &zero}); r.Verdict != flakegate.VerdictPass || len(r.Problems) != 0 || *r.ExitCode != 0 {
		t.Errorf("a gate that passed: %+v", r)
	}
	if r := merge(t, fixture(t), MergeOptions{ExitCode: &zero}); r.Verdict != flakegate.VerdictFail || !slices.Equal(codes(r), []string{ProblemGateDisagrees}) {
		t.Errorf("a gate that passed over a failed record: %+v", r)
	}
	if r := merge(t, fixture(t), MergeOptions{ExitCode: &one}); len(r.Problems) != 0 {
		t.Errorf("a gate that failed with a record saying why: %+v", r.Problems)
	}
}

// TestMerge_AMissingLeg: a part of the run begin said to expect, with no
// record, is named in missing and as a problem, so a run that lost a leg
// is never a pass; with the gate failing too, it fails.
func TestMerge_AMissingLeg(t *testing.T) {
	dir := t.TempDir()
	if _, err := Begin(dir, "abc", []string{"fast", "win"}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	writeRecords(t, dir, map[string]any{"fast.json": passing("-full")})
	r := merge(t, dir, MergeOptions{})
	if r.Verdict != VerdictIncomplete || !slices.Equal(r.Missing, []string{"win"}) || !slices.Equal(codes(r), []string{ProblemMissing}) || r.Problems[0].Record != "win" {
		t.Errorf("a leg with no record: %+v", r)
	}
	if r.Started == nil || !r.Started.Equal(now.Add(-time.Hour)) || r.Commit != "abc" || !slices.Equal(r.Expected, []string{"fast", "win"}) {
		t.Errorf("begin's start, commit and expectations were not kept: %+v", r)
	}
	one := 1
	if r := merge(t, dir, MergeOptions{ExitCode: &one}); r.Verdict != flakegate.VerdictFail {
		t.Errorf("a missing leg and a failed gate: %s", r.Verdict)
	}
}

// TestMerge_MalformedRecords: a file that is not JSON, a kind this reader
// does not know and a schema it does not know are each named with a code
// and leave a run with nothing against it incomplete; none is dropped or
// read as a pass.
func TestMerge_MalformedRecords(t *testing.T) {
	dir := t.TempDir()
	writeRecords(t, dir, map[string]any{
		"fast.json":      passing("-full"),
		"truncated.json": `{"schema":1,"kind":"testgate","verdict":"pa`,
		"junk.json":      "go test: no such file",
		"other.json":     `{"schema":1,"kind":"lint"}`,
		"future.json":    `{"schema":2,"kind":"testgate","verdict":"pass"}`,
		"leg.json":       `{"schema":2,"kind":"leg"}`,
		"step.json":      `{"schema":2,"kind":"step"}`,
	})
	r := merge(t, dir, MergeOptions{})
	want := map[string]string{
		"junk.json": ProblemUnreadable, "truncated.json": ProblemUnreadable, "other.json": ProblemUnknownKind,
		"future.json": ProblemUnknownSchema, "leg.json": ProblemUnknownSchema, "step.json": ProblemUnknownSchema,
	}
	got := map[string]string{}
	for _, p := range r.Problems {
		got[p.Record] = p.Code
		if p.Detail == "" {
			t.Errorf("%s: a problem with no detail", p.Record)
		}
	}
	if r.Verdict != VerdictIncomplete || len(got) != len(want) {
		t.Fatalf("%s %+v", r.Verdict, r.Problems)
	}
	for file, code := range want {
		if got[file] != code {
			t.Errorf("%s: %q, want %q", file, got[file], code)
		}
	}
}

// TestMerge_AValueThisReaderDoesNotKnow: a verdict, outcome, failure kind
// or step a newer producer wrote is kept as written, named as a problem,
// and never taken for a pass.
func TestMerge_AValueThisReaderDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	flaky := passing("-full")
	flaky.Verdict = "quarantined"
	newOutcome := passing("-count=3")
	newOutcome.Failures = []flakegate.RunFailure{{Package: "x/a", Test: "TestX", Kind: "fuzz", Outcome: "retried"}}
	writeRecords(t, dir, map[string]any{
		"fast.json":   flaky,
		"repeat.json": newOutcome,
		"win.json":    oscheck.LegRecord{OS: "Windows", Verdict: "skipped", Step: "warmup"},
		"images.json": `{"schema":1,"kind":"step","name":"images","verdict":"waived"}`,
	})
	r := merge(t, dir, MergeOptions{})
	if r.Verdict != VerdictIncomplete {
		t.Errorf("verdict %s with %+v", r.Verdict, r.Problems)
	}
	var details []string
	for _, p := range r.Problems {
		if p.Code != ProblemUnknownValue {
			t.Errorf("problem %+v", p)
		}
		details = append(details, p.Detail)
	}
	for _, value := range []string{`"quarantined"`, `"retried"`, `"fuzz"`, `"skipped"`, `"warmup"`, `"waived"`} {
		if !strings.Contains(strings.Join(details, "\n"), value) {
			t.Errorf("%s is not named among %q", value, details)
		}
	}
	if r.Passes[0].Run.Verdict != "quarantined" || r.Legs[0].Leg.Step != "warmup" {
		t.Error("an unknown value was not kept as written")
	}
}

// TestBegin_ARunThatHasNotEnded: begin's report says running, which no
// reader may take for a result, and names what it expects; a merge that
// never comes leaves it saying so. A name that could not be a record is
// refused.
func TestBegin_ARunThatHasNotEnded(t *testing.T) {
	dir := t.TempDir()
	if _, err := Begin(dir, "abc", []string{"fast", "mac"}, now); err != nil {
		t.Fatal(err)
	}
	r, err := ReadReport(filepath.Join(dir, ReportFile))
	if err != nil || r.Verdict != VerdictRunning || r.Started == nil || !slices.Equal(r.Expected, []string{"fast", "mac"}) {
		t.Errorf("%v %+v", err, r)
	}
	if _, err := Begin(dir, "abc", []string{"../x"}, now); err == nil {
		t.Error("an expected name that could not be a record was taken")
	}
	if _, err := Begin(filepath.Join(dir, "absent"), "abc", nil, now); err == nil {
		t.Error("a begin with nowhere to write it passed")
	}
}

// TestMerge_AnInterruptedRun: the merge an interrupt makes says
// interrupted, keeps what ran, names what did not as missing, and says
// why; a merge after a report that is not a running one starts afresh.
func TestMerge_AnInterruptedRun(t *testing.T) {
	dir := t.TempDir()
	if _, err := Begin(dir, "abc", []string{"fast", "repeat", "mac"}, now); err != nil {
		t.Fatal(err)
	}
	writeRecords(t, dir, map[string]any{"fast.json": passing("-full")})
	one := 1
	r := merge(t, dir, MergeOptions{Interrupted: true, ExitCode: &one})
	if r.Verdict != VerdictInterrupted || len(r.Passes) != 1 || !slices.Equal(r.Missing, []string{"repeat", "mac"}) {
		t.Errorf("%+v", r)
	}
	if want := []string{ProblemMissing, ProblemMissing, ProblemInterrupted}; !slices.Equal(codes(r), want) {
		t.Errorf("problems %v, want %v (an interrupted run's exit is no news)", codes(r), want)
	}
	if err := WriteJSON(filepath.Join(dir, ReportFile), r); err != nil {
		t.Fatal(err)
	}
	if again := merge(t, dir, MergeOptions{}); again.Started != nil || len(again.Expected) != 0 {
		t.Errorf("a merge over a finished report took its expectations: %+v", again)
	}
}

// TestDecodeReport_WhatAReaderAccepts: a report with fields this reader
// does not know (a newer producer of the same major version) reads, its
// known fields intact; another kind, another major version and text that
// is not a report are refused, each saying which.
func TestDecodeReport_WhatAReaderAccepts(t *testing.T) {
	newer := `{"schema":2,"kind":"pr-check","verdict":"pass","generated":"2026-10-19T12:00:00Z","dashboard_url":"https://x",
		"passes":[{"name":"fast","shard":3,"run":{"schema":1,"kind":"testgate","verdict":"pass","label":"-full","flakiness":0.1}}]}`
	r, err := DecodeReport([]byte(newer))
	if err != nil || r.Verdict != flakegate.VerdictPass || len(r.Passes) != 1 || r.Passes[0].Run.Label != "-full" {
		t.Errorf("a newer report of the same major version: %v %+v", err, r)
	}
	for name, tc := range map[string]struct{ data, want string }{
		"another kind":          {`{"schema":2,"kind":"leg"}`, `kind "leg"`},
		"a newer major version": {`{"schema":3,"kind":"pr-check"}`, "schema 3, where this reader knows 2"},
		"the old major version": {`{"schema":1,"kind":"pr-check","problems":["x"]}`, "schema 1"},
		"not JSON":              {`{`, "reading a report"},
		"a field of a new type": {`{"schema":2,"kind":"pr-check","verdict":7}`, "reading a report"},
	} {
		if _, err := DecodeReport([]byte(tc.data)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := ReadReport(filepath.Join(t.TempDir(), ReportFile)); !errors.Is(err, ErrNoReport) {
		t.Errorf("no report: %v", err)
	}
	if _, err := ReadReport(t.TempDir()); err == nil || errors.Is(err, ErrNoReport) {
		t.Errorf("a report that is a directory: %v", err)
	}
}

// TestMerge_ARoundTrip: what Merge returns is what the file it is written
// to reads back as.
func TestMerge_ARoundTrip(t *testing.T) {
	dir := fixture(t)
	one := 1
	r := merge(t, dir, MergeOptions{Commit: "abc", ExitCode: &one})
	path := filepath.Join(dir, ReportFile)
	if err := WriteJSON(path, r); err != nil {
		t.Fatal(err)
	}
	back, err := ReadReport(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(r)
	b, _ := json.Marshal(back)
	if string(a) != string(b) {
		t.Errorf("wrote\n%s\nread back\n%s", a, b)
	}
	if err := WriteJSON(filepath.Join(dir, "absent", "x.json"), r); err == nil {
		t.Error("a write with nowhere to go passed")
	}
	if err := WriteJSON(filepath.Join(dir, "x.json"), func() {}); err == nil {
		t.Error("a value JSON cannot hold was written")
	}
}

// TestWriteStep: a step is recorded by its exit code, or as not run with
// why; a name that would not be its own file is refused.
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
		if err := WriteStep(dir, name, tc.exit, tc.notRun); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var s Step
		if err := json.Unmarshal(data, &s); err != nil || s.Verdict != tc.verdict || s.ExitCode != tc.exit || s.Reason != tc.notRun ||
			s.Kind != StepKind || s.Schema != StepSchema || s.Name != name {
			t.Errorf("%s: %v %+v", name, err, s)
		}
	}
	for _, bad := range []string{"", "report", "../x", "a.b", "a/b", "a b"} {
		if err := WriteStep(dir, bad, 0, ""); err == nil {
			t.Errorf("the step name %q was taken", bad)
		}
	}
}

// TestMerge_AnUnreadableRecordFile: a record the merge cannot open is a
// problem, not an error ending the merge.
func TestMerge_AnUnreadableRecordFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "fast.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := os.Symlink(filepath.Join(dir, "absent"), filepath.Join(dir, "repeat.json"))
	need.Require(t, "symlinks", err == nil, "this machine cannot make a symbolic link here")
	r := merge(t, dir, MergeOptions{})
	if !slices.Equal(codes(r), []string{ProblemUnreadable}) || r.Problems[0].Record != "repeat.json" {
		t.Errorf("%+v", r.Problems)
	}
}

// TestMerge_EdgesOfWhatItReads: an expected leg and step that are present
// are not missing; a step record whose field has another type is
// unreadable; a record pr-check does not name sorts after those it does;
// and a report that cannot replace what is at its path is an error.
func TestMerge_EdgesOfWhatItReads(t *testing.T) {
	dir := fixture(t)
	if _, err := Begin(dir, "abc", []string{"mac", "coverage", "fast"}, now); err != nil {
		t.Fatal(err)
	}
	writeRecords(t, dir, map[string]any{
		"durations.json": `{"schema":1,"kind":"step","name":"durations","exit_code":"none","verdict":"pass"}`,
		"extra.json":     passing("-full"),
	})
	r := merge(t, dir, MergeOptions{})
	if len(r.Missing) != 0 {
		t.Errorf("present parts read as missing: %v", r.Missing)
	}
	if !slices.Equal(codes(r), []string{ProblemUnreadable}) || r.Problems[0].Record != "durations.json" {
		t.Errorf("problems %+v", r.Problems)
	}
	if last := r.Passes[len(r.Passes)-1].Name; last != "extra" {
		t.Errorf("a pass pr-check does not name sorted to %v", r.Passes)
	}

	blocked := filepath.Join(t.TempDir(), "report.json")
	if err := os.MkdirAll(filepath.Join(blocked, "held"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(blocked, r); err == nil {
		t.Error("a report written over a directory was reported written")
	}
}
