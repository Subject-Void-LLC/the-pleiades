// Tests of record-actions through a GitHub that answers from fixtures: the
// jobs of a run, each job's log (excerpts of real ones), and a pull
// request's latest run.
package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// fakeGitHub answers each path from a map, and errs on any other.
type fakeGitHub map[string]string

// API answers path.
func (f fakeGitHub) API(path string) ([]byte, error) {
	body, ok := f[path]
	if !ok {
		return nil, fmt.Errorf("no fixture for %s", path)
	}
	return []byte(body), nil
}

// PRChecks answers pull request pr's checks from the fixture "checks/<pr>".
func (f fakeGitHub) PRChecks(pr int) ([]byte, error) {
	return f.API(fmt.Sprintf("checks/%d", pr))
}

// runFixture is a run of four jobs: one that failed on a real test, one
// that failed on a strict one, a coverage job that failed because they did,
// one that passed with a contention, and one skipped.
func runFixture(t *testing.T) fakeGitHub {
	const r = "repos/" + repo + "/actions/"
	jobs := `{"jobs":[
		{"id":11,"run_id":7,"name":"containers (3)","conclusion":"failure","completed_at":"2026-10-24T01:22:57Z","labels":["ubuntu-24.04"],"head_sha":"` + testCommit + `","head_branch":"feat/x"},
		{"id":12,"run_id":7,"name":"fast","conclusion":"failure","completed_at":"2026-10-24T07:01:47Z","labels":["ubuntu-24.04"],"head_sha":"` + testCommit + `","head_branch":"feat/x"},
		{"id":13,"run_id":7,"name":"coverage","conclusion":"failure","completed_at":"2026-10-24T07:11:23Z","labels":["ubuntu-24.04"],"head_sha":"` + testCommit + `","head_branch":"feat/x"},
		{"id":14,"run_id":7,"name":"fast-macos","conclusion":"success","completed_at":"2026-10-24T07:09:55Z","labels":["macos-latest"],"head_sha":"` + testCommit + `","head_branch":"feat/x"},
		{"id":15,"run_id":7,"name":"nightly","conclusion":"skipped","labels":["ubuntu-24.04"],"head_sha":"` + testCommit + `"}]}`
	return fakeGitHub{
		r + "runs/7/jobs?per_page=100": jobs,
		r + "jobs/11/logs":             readFixture(t, "actions-containers-real.log"),
		r + "jobs/12/logs":             readFixture(t, "actions-fast-strict.log"),
		r + "jobs/13/logs":             readFixture(t, "actions-coverage.log"),
		r + "jobs/14/logs":             readFixture(t, "testgate-contention.log"),
		"repos/" + repo + "/pulls/89":  `{"head":{"sha":"` + testCommit + `"}}`,
		r + "runs?head_sha=" + testCommit + "&per_page=50": `{"workflow_runs":[
			{"id":6,"status":"completed","created_at":"2026-10-24T01:02:54Z"},
			{"id":8,"status":"in_progress","created_at":"2026-10-24T09:00:00Z"},
			{"id":7,"status":"completed","created_at":"2026-10-24T06:49:10Z"}]}`,
	}
}

// TestRecordActions_ARun: each job's failures and contention become
// entries, the coverage job with no test to blame is a leg, a skipped job
// is not read, and a second recording adds nothing.
func TestRecordActions_ARun(t *testing.T) {
	gh, dir := runFixture(t), t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"record-actions", "-dir", dir, "-pr", "89"}, &stdout, &stderr, gh); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	entries, err := readLedger(ledgerFile(dir, SourceActions))
	if err != nil {
		t.Fatal(err)
	}
	want := []brief{
		{KindTest, "containers (3)", "", "linux", "tests/e2e", "TestPackagingReleaseGate_BuildContextIsBoundedAndExcludesWhatItMust", VerdictReal, ""},
		{KindTest, "fast", "", "linux", "internal/catalog/net/cli", "TestOpenSession_SendsEveryConfigLineOverOneSession", VerdictStrict, ""},
		{KindLeg, "coverage", "", "linux", "", "", VerdictFail, "fast: failure, containers: success; the floors are checked only from jobs that passed"},
		{KindTest, "fast-macos", "", "macOS", "internal/catalog/wait", "TestRoutes_WaitsForARouteOSPFLearns", VerdictContention, ""},
	}
	if b := briefs(entries); !equalBriefs(b, want) {
		t.Errorf("\n got %+v\nwant %+v", b, want)
	}
	if e := entries[0]; e.RunID != 7 || e.JobID != 11 || e.Commit != testCommit || e.Branch != "feat/x" || e.SeenAt.IsZero() || e.Source != SourceActions {
		t.Errorf("the job's details: %+v", e)
	}
	stdout.Reset()
	if code := run([]string{"record-actions", "-dir", dir, "-run", "7", "-failed-only"}, &stdout, &stderr, gh); code != 0 || !strings.Contains(stdout.String(), "0 of 3 entries new") {
		t.Errorf("a second, failed-only recording: exit %d, %s %s", code, stdout.String(), stderr.String())
	}
}

// TestRecordActions_Refusals: a missing or doubled run choice is a usage
// error, and GitHub's own failure is reported.
func TestRecordActions_Refusals(t *testing.T) {
	gh := runFixture(t)
	for _, args := range [][]string{{"record-actions"}, {"record-actions", "-run", "7", "-pr", "89"}} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}, gh); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	for _, args := range [][]string{{"record-actions", "-run", "9"}, {"record-actions", "-pr", "90"}} {
		var stderr bytes.Buffer
		if code := run(append(args, "-dir", t.TempDir()), &bytes.Buffer{}, &stderr, gh); code != 1 || !strings.Contains(stderr.String(), "no fixture") {
			t.Errorf("%v: exit %d, %s", args, code, stderr.String())
		}
	}
	none := fakeGitHub{"repos/" + repo + "/pulls/1": `{"head":{"sha":"abc"}}`, "repos/" + repo + "/actions/runs?head_sha=abc&per_page=50": `{"workflow_runs":[]}`}
	if _, err := latestRunForPR(none, 1); err == nil || !strings.Contains(err.Error(), "no completed run") {
		t.Errorf("a pull request with no run: %v", err)
	}
	if _, err := latestRunForPR(fakeGitHub{"repos/" + repo + "/pulls/2": `{}`}, 2); err == nil {
		t.Error("a pull request with no head was read")
	}
}

// TestFromJob_CancelledAndUnexplained: a cancelled job with nothing to
// blame did not run, and a failed one with no log says its conclusion.
func TestFromJob_CancelledAndUnexplained(t *testing.T) {
	cancelled := fromJob(job{ID: 1, Name: "containers (1)", Conclusion: "cancelled", HeadSHA: testCommit}, jobLog{}, testAt)
	if len(cancelled) != 1 || cancelled[0].Verdict != VerdictNotRun || !strings.Contains(cancelled[0].Reason, "cancelled") {
		t.Errorf("cancelled: %+v", cancelled)
	}
	if got := runnerOS([]string{"windows-latest"}); got != "Windows" {
		t.Errorf("a Windows runner is %q", got)
	}
	if got := runnerOS([]string{"self-hosted"}); got != "self-hosted" {
		t.Errorf("another runner is %q", got)
	}
	if got := runnerOS(nil); got != "" {
		t.Errorf("no labels is %q", got)
	}
}
