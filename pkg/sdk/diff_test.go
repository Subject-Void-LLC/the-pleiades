package sdk_test

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// recordingContext is a RunbookContext that keeps what was written, so a
// test can read back the exact shape a rollback engine would later have
// to parse.
type recordingContext struct {
	stats   map[string]any
	statErr error
}

func newRecordingContext() *recordingContext {
	return &recordingContext{stats: map[string]any{}}
}

func (c *recordingContext) InjectSecrets() map[string]string { return nil }

func (c *recordingContext) SetStat(key string, value any) error {
	if c.statErr != nil {
		return c.statErr
	}
	c.stats[key] = value
	return nil
}

func (c *recordingContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// diffOf reads back the two halves of the recorded diff stat, failing if
// the shape is not what a reader would expect.
func diffOf(t *testing.T, c *recordingContext) (before, after map[string]any) {
	t.Helper()
	raw, ok := c.stats[sdk.StatDiff]
	if !ok {
		t.Fatalf("no %q stat was recorded", sdk.StatDiff)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%q stat is %T, want a map", sdk.StatDiff, raw)
	}
	before, _ = m[sdk.DiffBefore].(map[string]any)
	after, _ = m[sdk.DiffAfter].(map[string]any)
	if before == nil || after == nil {
		t.Fatalf("%q stat = %v, want both %q and %q maps", sdk.StatDiff, m, sdk.DiffBefore, sdk.DiffAfter)
	}
	return before, after
}

// TestRecordDiff proves the recorded shape is the one the contract
// promises: one stat, two named halves, both always present.
//
// The shape is the contract. A rollback engine reads it by name, so a
// module that wrote a before with no after, or used a different key,
// would produce a journal entry nothing can act on, and nothing would
// notice until a rollback was attempted.
func TestRecordDiff(t *testing.T) {
	c := newRecordingContext()

	err := sdk.RecordDiff(c, sdk.Diff{
		Before: map[string]any{"mode": "0644", "exists": true},
		After:  map[string]any{"mode": "0600", "exists": true},
	})
	if err != nil {
		t.Fatalf("RecordDiff: %v", err)
	}

	before, after := diffOf(t, c)
	if before["mode"] != "0644" {
		t.Errorf("before[mode] = %v, want 0644", before["mode"])
	}
	if after["mode"] != "0600" {
		t.Errorf("after[mode] = %v, want 0600", after["mode"])
	}
}

// TestRecordDiff_NilHalvesBecomeEmptyMaps proves a half nobody filled in
// is still readable.
//
// A nil map and an absent key look the same to a reader decoding JSON,
// and "there was nothing there before" is a real answer a rollback needs
// (it means the inverse is to remove what was created). Writing an empty
// map rather than nil keeps that answer distinguishable from a module
// that recorded nothing at all.
func TestRecordDiff_NilHalvesBecomeEmptyMaps(t *testing.T) {
	c := newRecordingContext()

	if err := sdk.RecordDiff(c, sdk.Diff{After: map[string]any{"exists": true}}); err != nil {
		t.Fatalf("RecordDiff: %v", err)
	}

	before, after := diffOf(t, c)
	if len(before) != 0 {
		t.Errorf("before = %v, want an empty map", before)
	}
	if after["exists"] != true {
		t.Errorf("after[exists] = %v, want true", after["exists"])
	}
}

// TestRecordDiff_ReportsAFailedWrite proves a context that cannot record
// is not silently ignored. A swallowed failure here would produce a run
// that looked complete and a journal that could not undo it.
func TestRecordDiff_ReportsAFailedWrite(t *testing.T) {
	c := newRecordingContext()
	c.statErr = errors.New("no")

	if err := sdk.RecordDiff(c, sdk.Diff{}); err == nil {
		t.Fatal("a failure to record the diff was swallowed")
	}
}

// TestUnchanged proves a converged run still records both halves, equal.
//
// Recording anything at all in that case is the point. A rollback engine
// has to tell "this task made no change, so undoing it means doing
// nothing" apart from "this task was never recorded", and an absent diff
// cannot express the first.
func TestUnchanged(t *testing.T) {
	c := newRecordingContext()
	state := map[string]any{"active": true}

	if err := sdk.RecordDiff(c, sdk.Unchanged(state)); err != nil {
		t.Fatalf("RecordDiff: %v", err)
	}

	before, after := diffOf(t, c)
	if before["active"] != true || after["active"] != true {
		t.Errorf("before = %v, after = %v, want both to hold the same state", before, after)
	}
}
