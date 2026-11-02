// Package sdk: what a check predicts about paths, and what earlier tasks in
// the same check predicted (Phase 46a, pkg/wire's predicted.go).
//
// A method's check that predicts a path gone or there records it with
// RecordPredicted; the engine keeps those per device for the rest of the
// check, and hands a later task's method the device's snapshot, which it reads
// through PredictedState. A method declares collection.Manifest.PredictsPaths
// when it records every path it changes: any other method whose check
// predicts a change leaves the device's snapshot unknown, since it may have
// changed a path nothing recorded.
package sdk

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// StatPredictedPaths is the stat key RecordPredicted writes: a list of
// {path, state}. The engine reads it only from a check, and refuses it on a
// real run, which predicts nothing.
const StatPredictedPaths = "predicted_paths"

// RecordPredicted records, in a check, the paths this call predicts gone
// (wire.PredictedAbsent) or there (wire.PredictedPresent). Each path must be
// absolute and cleaned; one that is not is refused rather than recorded,
// since the engine could not match it to another task's path. Call it once
// per call, with every path: a second call replaces the first.
func RecordPredicted(rc RunbookContext, paths ...wire.PredictedPath) error {
	record := make([]any, 0, len(paths))
	for _, p := range paths {
		if !wire.ValidPredictedPath(p) {
			return fmt.Errorf("sdk: a predicted path must be absolute, cleaned and absent or present, not %q (%s)", p.Path, p.State)
		}
		record = append(record, map[string]any{"path": p.Path, "state": p.State})
	}
	return rc.SetStat(StatPredictedPaths, record)
}

// RecordPredictionUnknown records, in a check, that this call changes a path
// it cannot name as the ledger keys paths (one not absolute, or not cleaned,
// whose real target only the device knows), so the device's predictions
// become unknown for the rest of the check rather than vouch for a path the
// real run may resolve differently.
func RecordPredictionUnknown(rc RunbookContext) error {
	return rc.SetStat(StatPredictedPaths, "unknown")
}

// ParsePredicted reads the paths a StatPredictedPaths stat holds, as a stat
// reads back in process ([]any of map[string]any, which RecordPredicted
// writes) and across a child process boundary (the same, through JSON). A
// value of any other shape, or a path ValidPredictedPath refuses, is an
// error: the engine then treats the device's predictions as unknown.
func ParsePredicted(v any) ([]wire.PredictedPath, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("sdk: %s is a %T, not a list", StatPredictedPaths, v)
	}
	out := make([]wire.PredictedPath, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("sdk: a %s entry is a %T, not a map", StatPredictedPaths, item)
		}
		p, _ := m["path"].(string)
		state, _ := m["state"].(string)
		pp := wire.PredictedPath{Path: p, State: state}
		if len(m) != 2 || !wire.ValidPredictedPath(pp) {
			return nil, fmt.Errorf("sdk: %q (%s) is not a predicted path", p, state)
		}
		out = append(out, pp)
	}
	return out, nil
}

// PredictedState is a RunbookContext that knows what earlier tasks in a check
// predicted for this device. A method reads it through PredictedAbsent, never
// by type assertion of its own, so a context without it answers as before.
type PredictedState interface {
	// Predictions returns the device's snapshot, nil when there is none (a
	// real run, the first task, or a context that keeps none).
	Predictions() *wire.Predictions
}

// PredictedAbsent reports whether an earlier task in this check predicted
// target gone (wire.Predictions.Absent): false whenever rc keeps no
// predictions, they are unknown, or none names target, so the method then
// reads the device as it is.
func PredictedAbsent(rc RunbookContext, target string) bool {
	state, ok := rc.(PredictedState)
	if !ok {
		return false
	}
	return state.Predictions().Absent(target)
}

// PredictedPresentUnder reports whether an earlier task in this check
// predicted a path made inside the directory dir
// (wire.Predictions.PresentUnder).
func PredictedPresentUnder(rc RunbookContext, dir string) bool {
	state, ok := rc.(PredictedState)
	if !ok {
		return false
	}
	return state.Predictions().PresentUnder(dir)
}

// PredictedAbsentUnder reports whether an earlier task in this check
// predicted any path inside the directory dir gone
// (wire.Predictions.AbsentUnder): when none did, no entry of dir can be, so a
// method need not list them to ask.
func PredictedAbsentUnder(rc RunbookContext, dir string) bool {
	state, ok := rc.(PredictedState)
	if !ok {
		return false
	}
	return state.Predictions().AbsentUnder(dir)
}

// predictionsKey marks a context carrying a device's predictions.
type predictionsKey struct{}

// WithPredictions returns ctx carrying p, the snapshot the engine hands the
// call it makes under ctx, for whatever builds that call's RunbookContext or
// child request to pass on. It stays in this process: a child is handed it on
// wire.ChildRequest.Predicted.
func WithPredictions(ctx context.Context, p *wire.Predictions) context.Context {
	return context.WithValue(ctx, predictionsKey{}, p)
}

// PredictionsFrom returns the snapshot WithPredictions put on ctx, nil when
// there is none.
func PredictionsFrom(ctx context.Context) *wire.Predictions {
	p, _ := ctx.Value(predictionsKey{}).(*wire.Predictions)
	return p
}
