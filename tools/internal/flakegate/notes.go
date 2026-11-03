// Reading the notes a test chose to report: a measured observation that is
// not a pass or a failure, such as how often a retry fired, so a run on a
// machine nobody can log in to (a CI runner) still shows it.
package flakegate

import (
	"regexp"
	"strings"
)

// Note is one line a test logged as a note, with the test that logged it.
type Note struct {
	// Package is the test's import path.
	Package string `json:"package"`
	// Test is the name of the test (or subtest) that logged the note.
	Test string `json:"test"`
	// Text is what followed "note: ", unmasked; a caller masks it before
	// printing it anywhere.
	Text string `json:"text"`
}

// noteLine matches a line t.Log printed whose message starts with "note: ",
// capturing the rest. go test prints a passing test's log lines only under
// -v or -json, and testgate always reads -json, so the note reaches it
// whether or not the test passed.
var noteLine = regexp.MustCompile(`^\s*\S+_test\.go:\d+: note: (.+)$`)

// Notes returns every note the run's tests logged, in the order they were
// printed. Only a test's own output counts: a package's output outside any
// test is go test's, not a note.
func Notes(events []Event) []Note {
	var notes []Note
	for _, evt := range events {
		if evt.Action != "output" || evt.Test == "" {
			continue
		}
		for _, line := range strings.Split(evt.Output, "\n") {
			if m := noteLine.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
				notes = append(notes, Note{Package: evt.Package, Test: evt.Test, Text: strings.TrimSpace(m[1])})
			}
		}
	}
	return notes
}

// NoteLine formats one note for a person, on one line.
func NoteLine(n Note) string {
	return shortPackage(n.Package) + " " + n.Test + ": " + n.Text
}
