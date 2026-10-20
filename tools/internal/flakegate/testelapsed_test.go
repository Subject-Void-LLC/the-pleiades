// Package flakegate: each test's own time, for a package divided by test.
package flakegate

import (
	"reflect"
	"testing"
)

// TestTestElapsed_TimesTopLevelTestsOfDividedPackages proves the times a
// divided package is balanced by: each top-level test's own pass, fail or
// skip, keyed by package and test, never a subtest's, a still-running
// test's, or a package's that is not divided.
func TestTestElapsed_TimesTopLevelTestsOfDividedPackages(t *testing.T) {
	events := []Event{
		{Action: "run", Package: "m/e2e", Test: "TestA"},
		{Action: "pass", Package: "m/e2e", Test: "TestA/sub", Elapsed: 1},
		{Action: "pass", Package: "m/e2e", Test: "TestA", Elapsed: 12.5},
		{Action: "fail", Package: "m/e2e", Test: "TestB", Elapsed: 300},
		{Action: "skip", Package: "m/e2e", Test: "TestC", Elapsed: 0},
		{Action: "pass", Package: "m/e2e", Elapsed: 330},
		{Action: "pass", Package: "m/whole", Test: "TestD", Elapsed: 4},
	}
	got := TestElapsed(events, func(pkg string) bool { return pkg == "m/e2e" })
	want := map[string]float64{"m/e2e TestA": 12.5, "m/e2e TestB": 300, "m/e2e TestC": 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TestElapsed = %v, want %v", got, want)
	}
}
