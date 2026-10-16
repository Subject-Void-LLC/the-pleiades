// Tests for the small pieces a gate's report is worded and ordered with.
package flakegate

import "testing"

// TestFailureKind_String names each kind as a report prints it.
func TestFailureKind_String(t *testing.T) {
	for kind, want := range map[FailureKind]string{
		FailedTest:    "failed",
		FailedBuild:   "build failed",
		FailedPackage: "the package's test binary failed or timed out",
	} {
		if got := kind.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", kind, got, want)
		}
	}
}

// TestHasCountFlag finds a count however it was written, so a re-run never
// appends a second one.
func TestHasCountFlag(t *testing.T) {
	for args, want := range map[string]bool{"-count=1": true, "-count": true, "-race": false, "": false} {
		if got := hasCountFlag([]string{"-race", args}); got != want {
			t.Errorf("hasCountFlag(%q) = %v, want %v", args, got, want)
		}
	}
}

// TestSortFailures orders by package, then test, so a report reads the
// same every run.
func TestSortFailures(t *testing.T) {
	fs := []Failure{{Package: "b", Test: "TestA"}, {Package: "a", Test: "TestZ"}, {Package: "a", Test: "TestB"}}
	sortFailures(fs)
	if fs[0].Test != "TestB" || fs[1].Test != "TestZ" || fs[2].Package != "b" {
		t.Errorf("sorted = %+v", fs)
	}
}
