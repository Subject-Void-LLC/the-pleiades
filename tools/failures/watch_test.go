// Tests of watch-pr through a GitHub that answers each poll from a script of
// gh pr checks outputs, in gh 2.4.0's own tab-separated format, and answers
// the record-actions that follows from the same fixtures record-actions'
// own tests use. No test waits: sleep is replaced by one that counts.
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scriptedGitHub answers API from fixtures and each PRChecks call with the
// next answer of its script, the last one again once the script runs out.
type scriptedGitHub struct {
	fakeGitHub
	answers []answer
	calls   int
}

// answer is one poll's output, or gh's error.
type answer struct {
	out string
	err error
}

// PRChecks answers the next poll.
func (s *scriptedGitHub) PRChecks(int) ([]byte, error) {
	a := s.answers[min(s.calls, len(s.answers)-1)]
	s.calls++
	return []byte(a.out), a.err
}

// checksLine is one line as gh 2.4.0 prints it when its output is not a
// terminal.
func checksLine(name, bucket, elapsed string) string {
	return name + "\t" + bucket + "\t" + elapsed + "\thttps://github.com/" + repo + "/actions/runs/7/job/1\n"
}

// noSleep replaces sleep for the test and returns the waits asked for.
func noSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var waits []time.Duration
	old := sleep
	sleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { sleep = old })
	return &waits
}

// watch runs watch-pr against gh and returns its exit code and output.
func watch(t *testing.T, gh github, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"watch-pr"}, args...), &stdout, &stderr, gh)
	return code, stdout.String(), stderr.String()
}

// TestWatchPR_UntilEveryCheckPasses: each check is printed once as it
// finishes, polls wait -every apart, and once none is pending the run is
// recorded and the report's two sections printed; every check passing (or
// skipped) exits 0.
func TestWatchPR_UntilEveryCheckPasses(t *testing.T) {
	waits := noSleep(t)
	gh := &scriptedGitHub{fakeGitHub: runFixture(t), answers: []answer{
		{out: checksLine("fast", "pending", "0") + checksLine("ci (ubuntu-24.04, true)", "pending", "0") + checksLine("nightly", "skipping", "0")},
		{out: checksLine("fast", "pending", "0") + checksLine("ci (ubuntu-24.04, true)", "pass", "4m36s") + checksLine("nightly", "skipping", "0")},
		{out: checksLine("fast", "pass", "13m43s") + checksLine("ci (ubuntu-24.04, true)", "pass", "4m36s") + checksLine("nightly", "skipping", "0")},
	}}
	dir := t.TempDir()
	code, out, stderr := watch(t, gh, "-pr", "89", "-every", "90s", "-dir", dir)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if gh.calls != 3 || len(*waits) != 2 || (*waits)[0] != 90*time.Second {
		t.Errorf("%d polls with waits %v, want 3 polls 90s apart", gh.calls, *waits)
	}
	for _, want := range []string{"pass      ci (ubuntu-24.04, true)", "pass      fast", "13m43s",
		"skipping  nightly", "3 passed or skipped, 0 did not", "entries new in", "Actions only", "Recurring contention"} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "ci (ubuntu-24.04, true)") != 1 || strings.Count(out, "nightly") != 1 {
		t.Errorf("a finished check was printed more than once:\n%s", out)
	}
	if _, err := os.Stat(ledgerFile(dir, SourceActions)); err != nil {
		t.Errorf("the run was not recorded: %v", err)
	}
}

// TestWatchPR_AFailedCheck: a check that failed, and one in a bucket this
// reader does not know, exit 1 naming both; a check re-run after failing is
// printed again when it finishes differently.
func TestWatchPR_AFailedCheck(t *testing.T) {
	noSleep(t)
	gh := &scriptedGitHub{fakeGitHub: runFixture(t), answers: []answer{
		{out: checksLine("containers (3)", "fail", "21m") + checksLine("coverage", "pending", "0")},
		{out: checksLine("containers (3)", "pending", "0") + checksLine("coverage", "pending", "0")},
		{out: checksLine("containers (3)", "pass", "20m") + checksLine("coverage", "fail", "49s") + checksLine("fast-macos", "cancel", "3m")},
	}}
	code, out, stderr := watch(t, gh, "-pr", "89", "-dir", t.TempDir())
	if code != 1 || !strings.Contains(stderr, "2 check(s) of pull request 89 did not pass: coverage (fail), fast-macos (cancel)") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(out, "fail      containers (3)") || !strings.Contains(out, "pass      containers (3)") {
		t.Errorf("a re-run check was not printed both times:\n%s", out)
	}
}

// TestWatchPR_WhenGitHubDoesNotAnswer: gh failing, printing no check yet, or
// printing a line that is not a check is asked again, said each time; five
// in a row end the wait with the last reason, and an answer in between
// resets the count.
func TestWatchPR_WhenGitHubDoesNotAnswer(t *testing.T) {
	noSleep(t)
	down := answer{err: errors.New("gh pr checks 89: exit status 1: could not resolve host")}
	gh := &scriptedGitHub{fakeGitHub: runFixture(t), answers: []answer{
		down, {out: ""}, {out: "garbage\n"}, down,
		{out: checksLine("fast", "pending", "0")},
		down, down, down, down,
		{out: checksLine("fast", "pass", "1m")},
	}}
	code, _, stderr := watch(t, gh, "-pr", "89", "-every", "1s", "-dir", t.TempDir())
	if code != 0 || gh.calls != 10 {
		t.Errorf("exit %d after %d polls: %s", code, gh.calls, stderr)
	}
	for _, want := range []string{"could not resolve host; asking again in 1s", "no check is reported yet", `"garbage"`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	never := &scriptedGitHub{fakeGitHub: runFixture(t), answers: []answer{down}}
	if code, _, stderr := watch(t, never, "-pr", "89", "-dir", t.TempDir()); code != 1 || never.calls != maxMisses || !strings.Contains(stderr, "gave up after 5 answers") {
		t.Errorf("gh never answering: exit %d after %d polls: %s", code, never.calls, stderr)
	}
}

// TestWatchPR_ARecordingThatFails: when the run cannot be recorded, watch-pr
// says so and still exits by what the checks said.
func TestWatchPR_ARecordingThatFails(t *testing.T) {
	noSleep(t)
	gh := &scriptedGitHub{fakeGitHub: fakeGitHub{}, answers: []answer{{out: checksLine("fast", "pass", "1m")}}}
	code, _, stderr := watch(t, gh, "-pr", "89", "-dir", t.TempDir())
	if code != 0 || !strings.Contains(stderr, "recording the run: no fixture") {
		t.Errorf("exit %d: %s", code, stderr)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := printWatchReport(&bytes.Buffer{}, file); err == nil {
		t.Error("a ledgers' directory that is a file was read")
	}
	broken := t.TempDir()
	if err := os.WriteFile(scopeFile(broken), []byte("{\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := printWatchReport(&bytes.Buffer{}, broken); err == nil {
		t.Error("a scope file that cannot be read was read")
	}
}

// TestWatchPR_Refusals: no pull request, a wait that is not positive and a
// stray argument are usage errors.
func TestWatchPR_Refusals(t *testing.T) {
	noSleep(t)
	gh := &scriptedGitHub{fakeGitHub: runFixture(t), answers: []answer{{out: checksLine("fast", "pass", "1m")}}}
	for _, args := range [][]string{{}, {"-pr", "0"}, {"-pr", "89", "-every", "0s"}, {"-pr", "89", "now"}, {"-pr", "eighty"}} {
		if code, _, stderr := watch(t, gh, args...); code != 2 {
			t.Errorf("%v: exit %d: %s", args, code, stderr)
		}
	}
	if gh.calls != 0 {
		t.Errorf("a refused command asked GitHub %d times", gh.calls)
	}
}

// TestParseChecks reads gh's lines whatever their line endings and however
// many fields, and refuses a line with no bucket.
func TestParseChecks(t *testing.T) {
	checks, err := parseChecks([]byte("fast\tpass\t1m\thttps://x\r\n\nlint\tfail\n"))
	if err != nil || len(checks) != 2 || checks[0].link != "https://x" || checks[0].elapsed != "1m" || checks[1].bucket != "fail" || checks[1].elapsed != "" {
		t.Errorf("%v %+v", err, checks)
	}
	for _, bad := range []string{"fast\n", "\tpass\n", "fast\t\n"} {
		if _, err := parseChecks([]byte(bad)); err == nil {
			t.Errorf("%q was read as a check", bad)
		}
	}
}
