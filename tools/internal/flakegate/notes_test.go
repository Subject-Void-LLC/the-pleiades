// Tests for Notes: which log lines count as a test's note.
package flakegate

import (
	"reflect"
	"testing"
)

// TestNotes_ReadsOnlyATestsOwnNoteLines: a "note: " line from a passing or
// a failing test is kept, with its test; an ordinary log line, a package's
// own output and a line that only mentions "note:" are not.
func TestNotes_ReadsOnlyATestsOwnNoteLines(t *testing.T) {
	const pkg = "github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	events := []Event{
		{Action: "output", Package: pkg, Test: "TestA", Output: "=== RUN   TestA\n"},
		{Action: "output", Package: pkg, Test: "TestA", Output: "    facts_test.go:376: note: lock opens tried again on darwin/arm64: 3\n"},
		{Action: "output", Package: pkg, Test: "TestA", Output: "    facts_test.go:377: an ordinary line\n"},
		{Action: "pass", Package: pkg, Test: "TestA"},
		{Action: "output", Package: pkg, Test: "TestB/case", Output: "    b_test.go:9: note: a subtest's note\r\n"},
		{Action: "output", Package: pkg, Test: "TestB/case", Output: "    b_test.go:10: it said note: inside\n"},
		{Action: "fail", Package: pkg, Test: "TestB/case"},
		{Action: "output", Package: pkg, Output: "    x_test.go:1: note: outside any test\n"},
		{Action: "start", Package: pkg, Test: "TestC", Output: "    c_test.go:1: note: not an output event\n"},
	}
	want := []Note{
		{Package: pkg, Test: "TestA", Text: "lock opens tried again on darwin/arm64: 3"},
		{Package: pkg, Test: "TestB/case", Text: "a subtest's note"},
	}
	got := Notes(events)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Notes = %+v, want %+v", got, want)
	}
	if line := NoteLine(got[0]); line != "internal/facts TestA: lock opens tried again on darwin/arm64: 3" {
		t.Fatalf("NoteLine = %q", line)
	}
	if Notes(nil) != nil {
		t.Fatal("no events, no notes")
	}
}
