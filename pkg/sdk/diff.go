package sdk

// The before-and-after recording every state-changing Collection method
// performs, and the reason it exists before anything reads it.
//
// A method that converges state has to answer two questions that look
// like one. "Did I change anything" decides whether the task reports
// changed, and a boolean answers it. "What was it before" decides whether
// anything can be undone, and only the forward run is in a position to
// answer that at all: once the change is applied, the prior state is
// gone.
//
// So a module records both, under one stat named "diff", holding a
// "before" map and an "after" map. That is Ansible's own shape, the one
// --diff renders, reused rather than reinvented per this platform's
// superset rule, which makes the recording dual-use: the values a
// rollback would need are the same values a diff view would show.
//
// Which "before" keys a given method's inverse consumes is decided by
// that method, in the params it passes to RecordInverse, not by any
// declaration beside it. (This said pkg/collection.Inverse.Captures
// until Phase 40 corrected it; no such type or field exists.) Nothing
// performs a rollback yet: the forward run records what would undo it,
// because only the forward run can capture the values an undo needs.

// StatDiff is the stat key the before-and-after record is written under.
const StatDiff = "diff"

// Keys inside the diff stat. They are Ansible's names, and they are
// constants because a module writing "Before" or "prev" instead would
// produce a record a rollback engine cannot read, and nothing would
// notice until a rollback was attempted.
const (
	DiffBefore = "before"
	DiffAfter  = "after"
)

// Diff is one method's record of what it found and what it left.
//
// Both halves are maps rather than typed structs because what is worth
// recording differs per method: a file's mode and owner, a service's
// active and enabled flags, a package's version. A rollback engine reads
// them by the names the method itself recorded, so the contract is per
// method rather than one shape every module has to fit. (This said
// Inverse.Captures until Phase 40 corrected it.)
type Diff struct {
	// Before is the state found on the device before acting. It is
	// recorded even when nothing changed, because "it was already like
	// this" is exactly what tells a rollback to do nothing.
	Before map[string]any

	// After is the state left behind. On an unchanged run it equals
	// Before, which is what makes a diff view show nothing rather than
	// showing an empty change.
	After map[string]any
}

// RecordDiff writes d under the "diff" stat.
//
// It is a function on this package rather than a method on the context
// so that the shape stays fixed: every module writes the same key with
// the same two sub-keys, and a module cannot half-record by writing a
// before with no after.
func RecordDiff(rc RunbookContext, d Diff) error {
	before := d.Before
	if before == nil {
		before = map[string]any{}
	}
	after := d.After
	if after == nil {
		after = map[string]any{}
	}
	return rc.SetStat(StatDiff, map[string]any{
		DiffBefore: before,
		DiffAfter:  after,
	})
}

// Unchanged returns a Diff whose two halves are the same state, for a
// method that found the device already converged.
//
// Recording anything at all in that case is deliberate. A rollback engine
// reading a journal needs to distinguish "this task made no change, so
// undoing it means doing nothing" from "this task was never recorded",
// and an absent diff cannot express the first.
func Unchanged(state map[string]any) Diff {
	return Diff{Before: state, After: state}
}
