// Tests that cannot run where cgo is off: go-sqlite3 links a stub there,
// and a test that opens a database fails on the stub's error rather than on
// anything it checks.
package flakegate

import "strings"

// CgoStub is the error go-sqlite3's stub returns when the binary was built
// with cgo off, as on a Windows machine with no C compiler.
const CgoStub = "go-sqlite3 requires cgo to work. This is a stub"

// CgoSkipReason is the skip reason a stubbed test is given, in
// testsupport.Require's "needs <what>: <why>" form, so Missing records the
// package as measured without cgo.
const CgoSkipReason = "needs cgo: go-sqlite3 is a stub in a build with cgo off (no C compiler), so this test cannot open its database here"

// CgoStubSkips returns events with each test that failed only on
// go-sqlite3's stub reported as a skip giving CgoSkipReason, for a run built
// with cgo off. The test proved nothing either way: it never reached what it
// checks.
//
// A test counts when its own output holds CgoStub and none of its subtests
// failed, or when every subtest that failed counts; a test that failed for
// any other reason, or beside a subtest that did, still fails. A package's
// own failure becomes a pass when every test that failed in it counts and
// it did not fail to build. Call it only for a run built with cgo off: with
// cgo on the stub is never linked, and the text could only come from a test
// that printed it.
func CgoStubSkips(events []Event) []Event {
	type key struct{ pkg, test string }
	failed := map[key]bool{}
	stubbed := map[key]bool{}
	buildFailed := map[string]bool{}
	for _, e := range events {
		k := key{e.Package, e.Test}
		switch {
		case e.Action == "output" && e.Test != "" && strings.Contains(e.Output, CgoStub):
			stubbed[k] = true
		case e.Action == "fail" && e.Test != "":
			failed[k] = true
		case e.Action == "build-fail" || e.FailedBuild != "":
			buildFailed[e.Package] = true
		}
	}

	// A failed test's failed subtests, by parent.
	children := map[key][]key{}
	for k := range failed {
		if i := strings.LastIndex(k.test, "/"); i > 0 {
			parent := key{k.pkg, k.test[:i]}
			children[parent] = append(children[parent], k)
		}
	}
	memo := map[key]bool{}
	var counts func(k key) bool
	counts = func(k key) bool {
		if v, ok := memo[k]; ok {
			return v
		}
		kids := children[k]
		v := len(kids) == 0 && stubbed[k]
		if len(kids) > 0 {
			v = true
			for _, c := range kids {
				if !counts(c) {
					v = false
					break
				}
			}
		}
		memo[k] = v
		return v
	}

	// A package passes once every test that failed in it counts.
	packagePasses := map[string]bool{}
	for k := range failed {
		if _, seen := packagePasses[k.pkg]; !seen {
			packagePasses[k.pkg] = !buildFailed[k.pkg]
		}
		if !counts(k) {
			packagePasses[k.pkg] = false
		}
	}

	// A counted test's own output is the stub's error, which Skips would
	// take for the reason; it is replaced by CgoSkipReason.
	out := make([]Event, 0, len(events))
	for _, e := range events {
		k := key{e.Package, e.Test}
		counted := e.Test != "" && failed[k] && counts(k)
		switch {
		case e.Action == "output" && counted:
			continue
		case e.Action == "fail" && counted:
			reason := e
			reason.Action, reason.Output, reason.Elapsed = "output", "    "+CgoSkipReason+"\n", 0
			out = append(out, reason)
			e.Action = "skip"
		case e.Action == "fail" && e.Test == "" && e.FailedBuild == "" && packagePasses[e.Package]:
			e.Action = "pass"
		}
		out = append(out, e)
	}
	return out
}
