// Test that a failure's cause survives in the run record when the test logs
// as much while it tears down as it did while it ran.
package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/flakegate"
)

// TestRecordFailureLines_KeepTheCauseBehindALoggedTeardown is the shape of
// tests/e2e's upgrade gate when it failed: the test files log every command
// they run, so the failure message is followed by about two hundred more
// lines from the same file (the fixture's teardown, each command logged). The
// last 40 lines the console shows are all teardown, and the line FailureOutput
// keeps as "the last the test's own file printed" is a teardown command, so
// a record kept to that budget held no word of why the test failed.
func TestRecordFailureLines_KeepTheCauseBehindALoggedTeardown(t *testing.T) {
	const pkg = "example.com/e2e"
	const cause = "    packaging_upgrade_compose_test.go:131: the controller did not answer the sign-in page"
	lines := []string{"=== RUN   TestGate", cause}
	for i := 0; i < 230; i++ {
		lines = append(lines, fmt.Sprintf("    packaging_upgrade_compose_test.go:%d: docker compose down (%dms)", 200+i, i))
	}
	lines = append(lines, "--- FAIL: TestGate (36.00s)")

	events := make([]flakegate.Event, 0, len(lines))
	for _, l := range lines {
		events = append(events, flakegate.Event{Action: "output", Package: pkg, Test: "TestGate", Output: l + "\n"})
	}
	f := flakegate.Failure{Package: pkg, Test: "TestGate"}

	if console := flakegate.FailureOutput(events, f, failureLines); strings.Contains(console, "did not answer the sign-in page") {
		t.Fatalf("the console budget already holds the cause, so this test no longer shows why the record needs more:\n%s", console)
	}
	record := flakegate.FailureOutput(events, f, recordFailureLines)
	if !strings.Contains(record, "did not answer the sign-in page") {
		t.Fatalf("the record's budget lost the failure's cause:\n%s", record)
	}
	if recordFailureLines <= failureLines {
		t.Fatalf("recordFailureLines %d must exceed failureLines %d", recordFailureLines, failureLines)
	}
}
