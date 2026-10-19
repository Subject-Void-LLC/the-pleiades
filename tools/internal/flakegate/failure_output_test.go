// Tests for FailureOutput's trimming: a tail of teardown must not hide the
// line that says why the test failed.
package flakegate

import (
	"strings"
	"testing"
)

// outputEvents turns lines into one test's output events.
func outputEvents(lines ...string) []Event {
	events := make([]Event, 0, len(lines))
	for _, l := range lines {
		events = append(events, Event{Action: "output", Package: "example.com/topology", Test: "TestLock", Output: l + "\n"})
	}
	return events
}

// TestFailureOutput_KeepsTheReasonBehindATailOfTeardown is the shape
// internal/topology printed: the failure, then a broker's log and the
// container's teardown, which filled the whole window.
func TestFailureOutput_KeepsTheReasonBehindATailOfTeardown(t *testing.T) {
	lines := []string{"=== RUN   TestLock", "    lock_test.go:88: lock bucket: context deadline exceeded"}
	for i := 0; i < 20; i++ {
		lines = append(lines, "            [1] 2026/09/30 20:35:13 [INF] broker line")
	}
	lines = append(lines, "2026/09/30 16:37:23 Stopping container", "2026/09/30 16:37:23 Container terminated", "--- FAIL: TestLock (121.41s)")

	out := FailureOutput(outputEvents(lines...), Failure{Package: "example.com/topology", Test: "TestLock"}, 5)
	if !strings.Contains(out, "lock_test.go:88: lock bucket: context deadline exceeded") {
		t.Fatalf("the failure's own line is missing:\n%s", out)
	}
	if !strings.HasSuffix(out, "--- FAIL: TestLock (121.41s)") || !strings.Contains(out, "the last the test's own file printed") {
		t.Fatalf("the tail or the note about the kept line is missing:\n%s", out)
	}
	if strings.Count(out, "broker line") != 2 {
		t.Fatalf("want only the tail's 2 broker lines, got:\n%s", out)
	}
}

// TestFailureOutput_IncludesTheFailingSubtest is the shape a table test
// fails in: its parent prints only its run and fail lines, and the reason
// is in a subtest's events, which carry the subtest's own name. A test
// whose name only begins the same way is not one of them.
func TestFailureOutput_IncludesTheFailingSubtest(t *testing.T) {
	pkg := "example.com/remotefile"
	events := []Event{
		{Action: "output", Package: pkg, Test: "TestPrediction", Output: "=== RUN   TestPrediction\n"},
		{Action: "output", Package: pkg, Test: "TestPrediction/a_setgid_directory", Output: "    predict_test.go:93: chmod: operation not permitted\n"},
		{Action: "output", Package: pkg, Test: "TestPredictionOther", Output: "    other_test.go:1: someone else's line\n"},
		{Action: "output", Package: pkg, Test: "TestPrediction", Output: "--- FAIL: TestPrediction (0.18s)\n"},
	}
	out := FailureOutput(events, Failure{Package: pkg, Test: "TestPrediction"}, 10)
	if !strings.Contains(out, "chmod: operation not permitted") || strings.Contains(out, "someone else's line") {
		t.Fatalf("want the subtest's reason and nothing of another test:\n%s", out)
	}
	if pkgOut := FailureOutput(events, Failure{Package: pkg}, 10); pkgOut != "" {
		t.Errorf("a package-level failure took the tests' lines:\n%s", pkgOut)
	}
}

// TestFailureOutput_ATailWithTheTestsOwnLineIsLeftAlone proves the kept
// line is added only when the tail has none of the test's own lines.
func TestFailureOutput_ATailWithTheTestsOwnLineIsLeftAlone(t *testing.T) {
	lines := []string{"    x_test.go:3: early"}
	for i := 0; i < 10; i++ {
		lines = append(lines, "    x_test.go:7: noise")
	}
	lines = append(lines, "    x_test.go:9: the real reason", "--- FAIL: TestLock (0.00s)")

	out := FailureOutput(outputEvents(lines...), Failure{Package: "example.com/topology", Test: "TestLock"}, 3)
	if strings.Contains(out, "early") || !strings.Contains(out, "the real reason") || strings.Count(out, "\n") != 3 {
		t.Fatalf("want the header and the last 3 lines only:\n%s", out)
	}
}
