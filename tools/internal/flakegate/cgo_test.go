// Tests for CgoStubSkips: which failures a cgo-less run reports as skips
// that need cgo, and which stay failures.
package flakegate

import (
	"reflect"
	"sort"
	"testing"
)

// stubLine is what a test that opened a database printed in a cgo-less
// build, as the Windows box printed it.
const stubLine = "    enttest.go:81: sqlite: check foreign_keys pragma: reading schema information Binary was compiled with 'CGO_ENABLED=0', " + CgoStub + "\n"

// testRun is a test's events: it runs, prints each line, and ends with
// action.
func testRun(pkg, test, action string, lines ...string) []Event {
	evs := []Event{{Action: "run", Package: pkg, Test: test}, {Action: "output", Package: pkg, Test: test, Output: "=== RUN   " + test + "\n"}}
	for _, l := range lines {
		evs = append(evs, Event{Action: "output", Package: pkg, Test: test, Output: l})
	}
	return append(evs, Event{Action: action, Package: pkg, Test: test})
}

// TestCgoStubSkips: a test that failed only on the stub, and a table test
// whose every failed case did, become skips that need cgo, with that
// reason; a test that failed otherwise, or beside a case that did, still
// fails; a package passes only when every failure in it was the stub; a
// build failure and a timeout are left alone.
func TestCgoStubSkips(t *testing.T) {
	var events []Event
	add := func(evs ...[]Event) {
		for _, e := range evs {
			events = append(events, e...)
		}
	}
	add(
		testRun("x/a", "TestStub", "fail", stubLine),
		testRun("x/a", "TestReal", "fail", "    a_test.go:9: want 1, got 2\n"),
		testRun("x/a", "TestTable/one", "fail", stubLine),
		testRun("x/a", "TestTable/two", "fail", stubLine),
		testRun("x/a", "TestTable", "fail", "--- FAIL: TestTable/one\n"),
		testRun("x/a", "TestMixed/stub", "fail", stubLine),
		testRun("x/a", "TestMixed/real", "fail", "    a_test.go:30: wrong\n"),
		testRun("x/a", "TestMixed", "fail"),
		testRun("x/a", "TestPasses", "pass"),
		[]Event{{Action: "fail", Package: "x/a"}},

		testRun("x/b", "TestStub", "fail", stubLine),
		[]Event{{Action: "output", Package: "x/b", Output: "FAIL\n"}, {Action: "fail", Package: "x/b"}},

		testRun("x/c", "TestStub", "fail", stubLine),
		[]Event{{Action: "fail", Package: "x/c", FailedBuild: "x/c [x/c.test]"}},

		[]Event{{Action: "output", Package: "x/d", Output: "panic: test timed out after 10m0s\n"}, {Action: "fail", Package: "x/d"}},
	)

	got := CgoStubSkips(events)

	var skipped []string
	for _, s := range Skips(got) {
		skipped = append(skipped, s.Package+" "+s.Test)
		if s.Reason != CgoSkipReason {
			t.Errorf("%s %s: reason %q", s.Package, s.Test, s.Reason)
		}
	}
	sort.Strings(skipped)
	want := []string{"x/a TestStub", "x/a TestTable", "x/a TestTable/one", "x/a TestTable/two", "x/a TestMixed/stub", "x/b TestStub", "x/c TestStub"}
	sort.Strings(want)
	if !reflect.DeepEqual(skipped, want) {
		t.Errorf("skipped %v, want %v", skipped, want)
	}

	hard, warned := Classify(got, nil)
	var failing []string
	for _, f := range append(hard, warned...) {
		failing = append(failing, f.Package+" "+f.Test)
	}
	sort.Strings(failing)
	wantFailing := []string{"x/a TestMixed", "x/a TestMixed/real", "x/a TestReal", "x/c ", "x/d "}
	if !reflect.DeepEqual(failing, wantFailing) {
		t.Errorf("failing %v, want %v", failing, wantFailing)
	}

	if m := Missing(Skips(got)); !reflect.DeepEqual(m["x/a"], []string{"cgo"}) || !reflect.DeepEqual(m["x/b"], []string{"cgo"}) {
		t.Errorf("Missing = %v, want cgo for x/a and x/b", m)
	}
	if len(CgoStubSkips(nil)) != 0 {
		t.Error("no events made some")
	}
}
