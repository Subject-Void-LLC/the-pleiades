package main

import (
	"strings"
	"testing"
)

// TestRunVersion_PrintsTheBuildVersion covers the one command in this
// binary that had no test at all. It matters slightly more than its size
// suggests: version is set through -ldflags at release time, so a change
// that broke this output would only ever be noticed by whoever is holding
// a release artifact and trying to find out what it is.
func TestRunVersion_PrintsTheBuildVersion(t *testing.T) {
	var err error
	out := captureStdout(t, func() { err = runVersion(nil) })
	if err != nil {
		t.Fatalf("runVersion() error = %v", err)
	}

	if !strings.HasPrefix(out, "pleiades ") {
		t.Errorf("runVersion() printed %q, want it to start with %q", out, "pleiades ")
	}
	if !strings.Contains(out, version) {
		t.Errorf("runVersion() printed %q, want it to contain the version %q", out, version)
	}
}

// TestRunVersion_RejectsUnknownFlag proves the command parses its
// arguments rather than ignoring them, so a typo is reported instead of
// silently printing the version as though the flag had been honored.
func TestRunVersion_RejectsUnknownFlag(t *testing.T) {
	// flag's ContinueOnError writes its own usage text to stderr, which is
	// noise here but harmless; only the returned error is asserted on.
	if err := runVersion([]string{"--not-a-real-flag"}); err == nil {
		t.Error("runVersion([--not-a-real-flag]) = nil, want a parse error")
	}
}
