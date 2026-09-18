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
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
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

// TestJobCanceller_ANilCancellerIsANilInterface is the identical guard for
// the cancel control, and it is needed for the identical reason.
//
// Deps.JobCanceller is a *dispatch.Canceller, so passing a nil one into an
// interface parameter yields a non-nil interface holding a nil pointer, the
// view's wiring check passes, and the first cancel dereferences it. This
// package's own conformance harness builds Deps without one, so the Jobs
// view really is registered against a nil canceller on every run of that
// suite.
func TestJobCanceller_ANilCancellerIsANilInterface(t *testing.T) {
	got := jobCanceller(Deps{})
	if got != nil {
		t.Fatalf("jobCanceller(Deps{}) = %#v, want an untyped nil; "+
			"a typed nil here passes the view's wiring check and panics on the first cancel", got)
	}
}

// TestJobCanceller_ARealCancellerIsPassedThrough is the other half.
//
// Without it the conversion could return nil unconditionally and still pass
// the guard above, which would withdraw the cancel control everywhere
// rather than only where it is unwired. That failure is quiet: a Cancel
// button that is simply never drawn looks like a design decision.
func TestJobCanceller_ARealCancellerIsPassedThrough(t *testing.T) {
	canceller := dispatch.NewCanceller(nil, nil, nil)
	if got := jobCanceller(Deps{JobCanceller: canceller}); got == nil {
		t.Fatal("jobCanceller dropped a real canceller, so no job could ever be stopped from the browser")
	}
}

// TestJobJournalAndLogArchive_ConvertBothWays covers the two conversions
// added beside jobRelauncher, for the identical reason.
//
// Each is three lines whose whole job is the typed-nil trap, and each has a
// branch a harness never reaches: the conformance suite wires neither port,
// so without this the non-nil half is declared and never executed, which is
// how a "simplification" back to a direct assignment would pass every test
// and then draw a Tasks tab that panics on its first read.
func TestJobJournalAndLogArchive_ConvertBothWays(t *testing.T) {
	// Absent, which is what the conformance harness and any composition
	// without a database or a broker really is.
	if got := jobJournal(Deps{}); got != nil {
		t.Errorf("jobJournal(no store) = %v, want an untyped nil", got)
	}
	if got := jobLogArchive(Deps{}); got != nil {
		t.Errorf("jobLogArchive(no broker) = %v, want an untyped nil", got)
	}

	// Present, which is every real Controller. The value has to arrive as
	// a usable interface rather than being dropped, or the Tasks tab and
	// the downloads are silently absent on a deployment that wired them.
	store := journal.NewEntStore(nil)
	if got := jobJournal(Deps{JobJournal: store}); got == nil {
		t.Error("jobJournal dropped a wired store, so the Tasks tab would never be drawn")
	}
	archive := api.NewLogArchive(nil)
	if got := jobLogArchive(Deps{JobLogs: archive}); got == nil {
		t.Error("jobLogArchive dropped a wired archive, so the log download would never be offered")
	}
}
