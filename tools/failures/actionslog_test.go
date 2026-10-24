// Tests of reading GitHub Actions job logs, against excerpts of real ones
// (testdata/actions-*.log, timestamps and all) and of testgate's own text.
package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// readFixture is a testdata file's text.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestParseJobLog_RealExcerpts: each real log's failures come out with the
// verdict testgate's heading gave them and the line that says why.
func TestParseJobLog_RealExcerpts(t *testing.T) {
	const mod = "github.com/Subject-Void-LLC/the-pleiades/"
	cases := map[string][]jobFailure{
		"actions-fast-strict.log": {{pkg: mod + "internal/catalog/net/cli", test: "TestOpenSession_SendsEveryConfigLineOverOneSession", verdict: VerdictStrict,
			err: "cli_session_internal_test.go:123: config over a real session: net.cli.config: netcli: remoteexec: read from shell on <ip>:35569: EOF"}},
		"actions-containers-real.log": {{pkg: mod + "tests/e2e", test: "TestPackagingReleaseGate_BuildContextIsBoundedAndExcludesWhatItMust", verdict: VerdictReal,
			err: "packaging_images_test.go:294: the build context is 32.6 MiB, over the 32.0 MiB ceiling; check .dockerignore for a rule that was removed or narrowed"}},
		"actions-containers-wait.log": {{pkg: mod + "internal/catalog/wait", test: "TestConnection_WaitsForANetworkDevicesPrompt", verdict: VerdictReal,
			err: `connection_test.go:101: the IOS device received ["terminal length 0" "terminal width 512"], want only IOS's paging command`}},
		"testgate-contention.log": {{pkg: mod + "internal/catalog/wait", test: "TestRoutes_WaitsForARouteOSPFLearns", verdict: VerdictContention}},
		"actions-coverage.log":    nil,
	}
	for file, want := range cases {
		got := parseJobLog(readFixture(t, file))
		if file == "testgate-contention.log" && len(got.failures) == 1 {
			if got.failures[0].err == "" || len(got.failures[0].err) > maxText {
				t.Errorf("%s: the contention's error line is %q", file, got.failures[0].err)
			}
			got.failures[0].err = ""
		}
		if !reflect.DeepEqual(got.failures, want) {
			t.Errorf("%s: failures\n got %+v\nwant %+v", file, got.failures, want)
		}
	}
	if got := parseJobLog(readFixture(t, "actions-coverage.log")).reason; got != "fast: failure, containers: success; the floors are checked only from jobs that passed" {
		t.Errorf("the coverage job's reason is %q", got)
	}
}

// TestParseJobLog_PackageFailuresAndHeadings: a package named by what
// failed has no test and keeps the words as its reason, each heading gives
// its own verdict, and a job's own error annotation is its reason.
func TestParseJobLog_PackageFailuresAndHeadings(t *testing.T) {
	log := "2026-10-24T01:00:00.1Z testgate: 2 failure(s) were NOT re-run, because more than 5 distinct tests failed:\n" +
		"2026-10-24T01:00:00.2Z \n" +
		"2026-10-24T01:00:00.3Z   x/a: the package's test binary failed or timed out\n" +
		"2026-10-24T01:00:00.4Z   x/b: TestB\n" +
		"2026-10-24T01:00:00.5Z \n" +
		"testgate: 1 failure(s), and -strict re-runs nothing:\n\n  x/c: build failed\n" +
		"##[error]The job running on runner X has exceeded the maximum execution time of 60 minutes.\n"
	got := parseJobLog(log)
	want := []jobFailure{
		{pkg: "x/a", reason: "the package's test binary failed or timed out", verdict: VerdictNotRerun},
		{pkg: "x/b", test: "TestB", verdict: VerdictNotRerun},
		{pkg: "x/c", reason: "build failed", verdict: VerdictStrict},
	}
	if !reflect.DeepEqual(got.failures, want) {
		t.Errorf("failures\n got %+v\nwant %+v", got.failures, want)
	}
	if got.reason != "The job running on runner X has exceeded the maximum execution time of 60 minutes." {
		t.Errorf("reason %q", got.reason)
	}
	if got := parseJobLog("nothing here\n").reason; got != "" {
		t.Errorf("a log with no error has the reason %q", got)
	}
	if got := jobReason([]string{"##[error]Process completed with exit code 2."}); got != "Process completed with exit code 2." {
		t.Errorf("an error with nothing before it has the reason %q", got)
	}
}

// TestFirstError_PicksTheLineThatSaysWhy: a test's own failing line wins
// over what it logged on the way, and output with no FAIL line still gives
// a line.
func TestFirstError_PicksTheLineThatSaysWhy(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"a later run fails": {"=== RUN   TestA\n--- PASS: TestA (0.0s)\n=== RUN   TestA\n    a_test.go:3: setup\n    a_test.go:9: got 1, want 2\n--- FAIL: TestA (0.0s)", "a_test.go:9: got 1, want 2"},
		"no failure words":  {"=== RUN   TestA\n    a_test.go:3: a dribble at login = <nil>\n--- FAIL: TestA", "a_test.go:3: a dribble at login = <nil>"},
		"only other output": {"=== RUN   TestA\nsome log line\n--- FAIL: TestA", "some log line"},
		"a panic":           {"panic: runtime error: index out of range\n\ngoroutine 1 [running]:", "panic: runtime error: index out of range"},
		"no FAIL line":      {"line one\nline two\nPASS", "line two"},
		"nothing":           {"", ""},
	}
	for name, c := range cases {
		if got := firstError(c.in); got != c.want {
			t.Errorf("%s: firstError = %q, want %q", name, got, c.want)
		}
	}
}
