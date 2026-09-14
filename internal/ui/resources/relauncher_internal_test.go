// This file covers one conversion, for a bug that does not look like one.
//
// Deps.Dispatcher is a *api.Dispatcher. Passing a nil one straight into an
// interface parameter produces a NON-nil interface value holding a nil
// pointer, so the receiving view's own "is this wired" check passes and the
// first call dereferences it. The conversion is three lines and exists
// entirely to stop that, which is exactly the kind of code that gets
// "simplified" back out again by somebody who reads it as redundant.
package resources

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
)

// TestJobRelauncher_ANilDispatcherIsANilInterface is the regression guard.
//
// A deployment with no dispatcher is real rather than hypothetical: this
// package's own conformance harness builds Deps without one, so the Jobs
// view is registered against a nil dispatcher every time that suite runs.
func TestJobRelauncher_ANilDispatcherIsANilInterface(t *testing.T) {
	got := jobRelauncher(Deps{})
	if got != nil {
		t.Fatalf("jobRelauncher(Deps{}) = %#v, want an untyped nil; "+
			"a typed nil here passes the view's wiring check and panics on the first relaunch", got)
	}
}

// TestJobRelauncher_ARealDispatcherIsPassedThrough is the other half.
//
// Without it the conversion could return nil unconditionally and still pass
// the guard above, which would disable relaunching everywhere instead of
// only where it is unwired.
func TestJobRelauncher_ARealDispatcherIsPassedThrough(t *testing.T) {
	dispatcher := &api.Dispatcher{}
	if got := jobRelauncher(Deps{Dispatcher: dispatcher}); got == nil {
		t.Fatal("jobRelauncher dropped a real dispatcher, so the relaunch control would never work")
	}
}
