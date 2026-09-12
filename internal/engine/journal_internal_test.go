// These tests reach the journal port's unexported parts from inside the
// package: the no-op default sink NewExecutor installs, and the option
// that replaces it.
//
// They belong here rather than in journal_entry_internal_test.go because
// they are about the SEAM rather than about the projection. A caller that
// wires no sink has to keep its exact prior behavior, and the only way to
// see that is to look at the field a caller cannot reach.
package engine

import (
	"context"
	"testing"
)

// recordingJournal is a Journal that keeps what it was handed, so a test
// can prove WithJournal wired the sink the Executor actually reaches
// rather than only that the option returned without error.
type recordingJournal struct {
	calls [][]JournalEntry
}

// Record implements Journal by appending entries to calls.
func (j *recordingJournal) Record(_ context.Context, entries []JournalEntry) error {
	j.calls = append(j.calls, entries)
	return nil
}

// newTestExecutor builds an Executor with nil dependencies, which is safe
// here and only here: NewExecutor stores its arguments and never calls
// through them, and none of the tests below runs a DAG. They are about
// option wiring alone, so supplying real fakes would add moving parts
// without adding evidence.
func newTestExecutor(t *testing.T, opts ...ExecutorOption) *Executor {
	t.Helper()
	return NewExecutor(nil, nil, nil, nil, nil, 0, opts...)
}

// TestNewExecutorDefaultsToANoopJournal proves the field is never nil for
// a caller that wires no sink. That is the property every pre-existing
// NewExecutor call site depends on: once Run records a level, a nil field
// would panic on the very first one.
func TestNewExecutorDefaultsToANoopJournal(t *testing.T) {
	x := newTestExecutor(t)

	if x.journal == nil {
		t.Fatal("NewExecutor left journal nil; every existing call site would panic at the first level boundary")
	}
	if _, ok := x.journal.(noopJournal); !ok {
		t.Fatalf("NewExecutor defaulted journal to %T, want noopJournal", x.journal)
	}
	if err := x.journal.Record(context.Background(), []JournalEntry{{NodeID: "tasks[0]"}}); err != nil {
		t.Fatalf("the default sink reported an error: %v; discarding is its contract, not a failure", err)
	}
}

// TestWithJournalInstallsTheSink proves the option replaces the default
// with the caller's own sink, and that entries handed to the field reach
// it unchanged.
func TestWithJournalInstallsTheSink(t *testing.T) {
	sink := &recordingJournal{}
	x := newTestExecutor(t, WithJournal(sink))

	if x.journal != Journal(sink) {
		t.Fatalf("WithJournal left journal as %T, want the supplied sink", x.journal)
	}
	if err := x.journal.Record(context.Background(), []JournalEntry{{NodeID: "tasks[0]"}}); err != nil {
		t.Fatalf("Record through the wired sink failed: %v", err)
	}
	if len(sink.calls) != 1 || len(sink.calls[0]) != 1 || sink.calls[0][0].NodeID != "tasks[0]" {
		t.Fatalf("the wired sink received %v, want one call carrying one entry for tasks[0]", sink.calls)
	}
}

// TestWithJournalNilKeepsTheDefault proves a nil sink leaves the no-op in
// place instead of installing a nil interface value. A composition root
// that builds its sink conditionally would otherwise turn "no journal
// configured" into a panic at the first level boundary, which is the one
// failure shape the default exists to rule out.
func TestWithJournalNilKeepsTheDefault(t *testing.T) {
	x := newTestExecutor(t, WithJournal(nil))

	if _, ok := x.journal.(noopJournal); !ok {
		t.Fatalf("WithJournal(nil) set journal to %T, want the noopJournal default left in place", x.journal)
	}
}
