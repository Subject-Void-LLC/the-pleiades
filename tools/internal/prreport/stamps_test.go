// Tests for the stamps that tie a report's records to one commit: a record
// left by an earlier run (another commit, or none) is stale, a run over
// uncommitted code is dirty, either keeps the report from passing, a check
// with no machine keeps its treatment, and the report's oldest record is
// what a receipt's age is counted from.
package prreport

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/oscheck"
)

// earlier is another commit, as a run before this one checked.
var earlier = strings.Repeat("cd34", 10)

// problemsByRecord is each problem's code, by the record it names.
func problemsByRecord(r Report) map[string][]string {
	out := map[string][]string{}
	for _, p := range r.Problems {
		out[p.Record] = append(out[p.Record], p.Code)
	}
	return out
}

// TestMerge_StaleAndDirtyRecords: a pass, leg and step stamped with an
// earlier commit are stale; records stamped with none are stale and not
// also dirty; a pass and a step stamped on a dirty tree are dirty; a check
// with no machine, which names no commit, is neither. Each is named with
// its record, and a run with nothing failed is incomplete; with a failure
// it fails.
func TestMerge_StaleAndDirtyRecords(t *testing.T) {
	dir := t.TempDir()
	old := passing("-count=3")
	old.Commit = earlier
	unstamped := passing("-full -p 1")
	unstamped.Commit, unstamped.Clean = "", false
	dirty := passing("-full")
	dirty.Clean = false
	writeRecords(t, dir, map[string]any{
		"fast.json":       dirty,
		"repeat.json":     old,
		"containers.json": unstamped,
		"mac.json":        oscheck.LegRecord{OS: "macOS", Commit: earlier, Verdict: flakegate.VerdictPass},
		"win.json":        oscheck.LegRecord{OS: "Windows", Verdict: flakegate.VerdictPass},
		"bsd.json":        oscheck.LegRecord{OS: "FreeBSD", Verdict: flakegate.VerdictNotRun, Step: oscheck.StepSettings, Error: "no machine"},
	})
	for name, s := range map[string]flakegate.Stamp{
		"govulncheck": {Commit: earlier, Clean: true},
		"coverage":    {Commit: commit},
		"images":      {},
		"sizes":       stamp,
	} {
		if err := WriteStep(dir, name, 0, "", s, now); err != nil {
			t.Fatal(err)
		}
	}
	r := merge(t, dir, MergeOptions{Commit: commit})
	want := map[string][]string{
		"fast.json":        {ProblemDirty},
		"repeat.json":      {ProblemStale},
		"containers.json":  {ProblemStale},
		"mac.json":         {ProblemStale},
		"win.json":         {ProblemStale},
		"govulncheck.json": {ProblemStale},
		"coverage.json":    {ProblemDirty},
		"images.json":      {ProblemStale},
	}
	got := problemsByRecord(r)
	for record, codes := range want {
		if !slices.Equal(got[record], codes) {
			t.Errorf("%s: %v, want %v", record, got[record], codes)
		}
	}
	if len(got) != len(want) {
		t.Errorf("problems %v, want exactly %v", got, want)
	}
	for _, p := range r.Problems {
		if p.Record == "repeat.json" && (!strings.Contains(p.Detail, earlier[:12]) || !strings.Contains(p.Detail, commit[:12])) {
			t.Errorf("a stale record's detail does not name both commits: %s", p.Detail)
		}
	}
	if r.Verdict != VerdictIncomplete {
		t.Errorf("verdict %s, want incomplete: nothing failed, and the records are not all this commit's", r.Verdict)
	}
	failed := passing("-full")
	failed.Verdict, failed.Commit = flakegate.VerdictFail, earlier
	writeRecords(t, dir, map[string]any{"fast.json": failed})
	if r := merge(t, dir, MergeOptions{Commit: commit}); r.Verdict != flakegate.VerdictFail {
		t.Errorf("a stale record that failed: %s", r.Verdict)
	}
}

// TestMerge_AStaleAndDirtyRecordIsBoth: a record from another commit whose
// tree was dirty is named for each, since running it again on a clean tree
// is what fixes both.
func TestMerge_AStaleAndDirtyRecordIsBoth(t *testing.T) {
	dir := t.TempDir()
	if err := WriteStep(dir, "govulncheck", 0, "", flakegate.Stamp{Commit: earlier}, now); err != nil {
		t.Fatal(err)
	}
	r := merge(t, dir, MergeOptions{Commit: commit})
	if got := codes(r); !slices.Equal(got, []string{ProblemStale, ProblemDirty}) {
		t.Errorf("problems %v, want stale then dirty", got)
	}
}

// TestMerge_AReportWithNoCommit: with no commit to tie them to, records are
// one stale problem for the report, not one per record, and are still
// dirty when stamped so; a report holding only a check with no machine, or
// nothing at all, has nothing to tie.
func TestMerge_AReportWithNoCommit(t *testing.T) {
	dir := t.TempDir()
	dirty := passing("-full")
	dirty.Clean = false
	writeRecords(t, dir, map[string]any{"fast.json": dirty, "repeat.json": passing("-count=3")})
	r := merge(t, dir, MergeOptions{})
	if got := problemsByRecord(r); !slices.Equal(got[""], []string{ProblemStale}) || !slices.Equal(got["fast.json"], []string{ProblemDirty}) || len(got) != 2 {
		t.Errorf("problems %v", got)
	}
	if r.Verdict != VerdictIncomplete {
		t.Errorf("verdict %s", r.Verdict)
	}
	unset := t.TempDir()
	writeRecords(t, unset, map[string]any{"bsd.json": oscheck.LegRecord{OS: "FreeBSD", Verdict: flakegate.VerdictNotRun, Step: oscheck.StepSettings}})
	if r := merge(t, unset, MergeOptions{}); len(r.Problems) != 0 || r.Verdict != flakegate.VerdictPass {
		t.Errorf("a check with no machine and no commit: %s %+v", r.Verdict, r.Problems)
	}
	if r := merge(t, t.TempDir(), MergeOptions{}); len(r.Problems) != 0 {
		t.Errorf("an empty directory: %+v", r.Problems)
	}
}

// TestMerge_TheOldestRecord: oldest is the earliest start of a pass or a
// leg, or the earliest step, whichever kind it is; a check with no machine
// and a record that does not say when are left out; a report of neither
// has none.
func TestMerge_TheOldestRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		records map[string]any
		step    time.Time
		want    time.Time
	}{
		"a pass": {map[string]any{"fast.json": passing("-full")}, now, now.Add(-time.Hour)},
		"a leg": {map[string]any{"fast.json": passing("-full"),
			"mac.json": oscheck.LegRecord{OS: "macOS", Commit: commit, Verdict: flakegate.VerdictPass, Started: now.Add(-3 * time.Hour)}}, now, now.Add(-3 * time.Hour)},
		"a step": {map[string]any{"fast.json": passing("-full")}, now.Add(-26 * time.Hour), now.Add(-26 * time.Hour)},
		"not a check with no machine": {map[string]any{"fast.json": passing("-full"),
			"bsd.json": oscheck.LegRecord{OS: "FreeBSD", Verdict: flakegate.VerdictNotRun, Step: oscheck.StepSettings, Started: now.Add(-48 * time.Hour)}}, now, now.Add(-time.Hour)},
		"not a time nobody wrote": {map[string]any{"fast.json": passing("-full")}, time.Time{}, now.Add(-time.Hour)},
	} {
		dir := t.TempDir()
		writeRecords(t, dir, tc.records)
		if err := WriteStep(dir, "coverage", 0, "", stamp, tc.step); err != nil {
			t.Fatal(err)
		}
		r := merge(t, dir, MergeOptions{Commit: commit})
		if r.Oldest == nil || !r.Oldest.Equal(tc.want) {
			t.Errorf("%s: oldest %v, want %v", name, r.Oldest, tc.want)
		}
	}
	if r := merge(t, t.TempDir(), MergeOptions{Commit: commit}); r.Oldest != nil {
		t.Errorf("a report with no records has an oldest: %v", r.Oldest)
	}
}
