// Package sdk: what a check predicts about paths, and what earlier tasks in
// the same check predicted (Phase 46a, pkg/wire's predicted.go).
//
// A method's check that predicts a path gone or there records it with
// RecordPredicted, as the device resolves it (pkg/remotefile's Locate); the
// engine keeps those per device for the rest of the check, and hands a later
// task's method the device's snapshot, which it reads through PredictedState.
// A method declares collection.Manifest.PredictsPaths when its check records
// every path it changes. The engine keeps a device's predictions only while
// every task on it is such a check that succeeded: any other task, a skipped
// one, a failure, or a check whose prediction could not be recorded drops
// them, and the device is read as it is again.
package sdk

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// StatPredictedPaths is the stat key RecordPredicted writes: a list of
// {path, state, dirs}. The engine reads it only from a check, and refuses it
// on a real run, which predicts nothing.
const StatPredictedPaths = "predicted_paths"

// RecordPredicted records, in a check, the paths this call predicts gone
// (wire.PredictedAbsent) or there (wire.PredictedPresent), each as the device
// resolves it. One that wire.ValidPredictedPath refuses is refused rather
// than recorded. Call it once per call, with every path: a second call
// replaces the first.
func RecordPredicted(rc RunbookContext, paths ...wire.PredictedPath) error {
	record := make([]any, 0, len(paths))
	for _, p := range paths {
		if !wire.ValidPredictedPath(p) {
			return fmt.Errorf("sdk: a predicted path must be a physical path, absent or present, with valid directory identities, not %q (%s)", p.Path, p.State)
		}
		dirs := make([]any, 0, len(p.Dirs))
		for _, d := range p.Dirs {
			dirs = append(dirs, map[string]any{"path": d.Path, "id": d.ID})
		}
		record = append(record, map[string]any{"path": p.Path, "state": p.State, "dirs": dirs})
	}
	return rc.SetStat(StatPredictedPaths, record)
}

// RecordPredictionUnknown records, in a check, that this call changes a path
// it cannot name as the device resolves it, so the engine drops the device's
// predictions rather than vouch for a path the real run may meet differently.
func RecordPredictionUnknown(rc RunbookContext) error {
	return rc.SetStat(StatPredictedPaths, "unknown")
}

// ParsePredicted reads the paths a StatPredictedPaths stat holds, as a stat
// reads back in process ([]any of map[string]any, which RecordPredicted
// writes) and across a child process boundary (the same, through JSON). A
// value of any other shape, or a path ValidPredictedPath refuses, is an
// error: the engine then drops the device's predictions.
func ParsePredicted(v any) ([]wire.PredictedPath, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("sdk: %s is a %T, not a list", StatPredictedPaths, v)
	}
	out := make([]wire.PredictedPath, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok || len(m) != 3 {
			return nil, fmt.Errorf("sdk: a %s entry is not a map of path, state and dirs", StatPredictedPaths)
		}
		p, _ := m["path"].(string)
		state, _ := m["state"].(string)
		dirs, err := parseDirs(m["dirs"])
		if err != nil {
			return nil, err
		}
		pp := wire.PredictedPath{Path: p, State: state, Dirs: dirs}
		if !wire.ValidPredictedPath(pp) {
			return nil, fmt.Errorf("sdk: %q (%s) is not a predicted path", p, state)
		}
		out = append(out, pp)
	}
	return out, nil
}

// parseDirs reads a record's directory identities.
func parseDirs(v any) ([]wire.DirID, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("sdk: a %s entry's dirs is a %T, not a list", StatPredictedPaths, v)
	}
	out := make([]wire.DirID, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		p, pathOK := m["path"].(string)
		id, idOK := m["id"].(string)
		if !ok || len(m) != 2 || !pathOK || !idOK {
			return nil, fmt.Errorf("sdk: a %s directory is not a map of path and id", StatPredictedPaths)
		}
		out = append(out, wire.DirID{Path: p, ID: id})
	}
	return out, nil
}

// PredictedState is a RunbookContext that knows what earlier tasks in a check
// predicted for this device. A method reads it through the functions below,
// never by type assertion of its own, so a context without it answers as
// before.
type PredictedState interface {
	// Predictions returns the device's snapshot, nil when the run keeps none
	// (a real run, or a context that keeps none).
	Predictions() *wire.Predictions
}

// predictions returns rc's snapshot, nil when it keeps none.
func predictions(rc RunbookContext) *wire.Predictions {
	if state, ok := rc.(PredictedState); ok {
		return state.Predictions()
	}
	return nil
}

// PredictionsWanted reports whether this call runs in a check that keeps
// predictions, so a method should read where its paths resolve and record
// them. Outside one, a method need not pay for that read.
func PredictionsWanted(rc RunbookContext) bool { return predictions(rc) != nil }

// PredictedAbsent reports whether an earlier task in this check predicted l
// gone (wire.Predictions.Absent): false whenever rc keeps no predictions or
// none vouches for l, so the method then reads the device as it is.
func PredictedAbsent(rc RunbookContext, l wire.Location) bool {
	return predictions(rc).Absent(l)
}

// PredictedPresent reports whether an earlier task in this check made l
// itself (wire.Predictions.Present), so the device as it is now lacks what
// this task meets.
func PredictedPresent(rc RunbookContext, l wire.Location) bool {
	return predictions(rc).Present(l)
}

// PredictedFresh reports whether an earlier task in this check made l where
// the real run had nothing (wire.Predictions.Fresh), so it holds only what
// earlier tasks put under it.
func PredictedFresh(rc RunbookContext, l wire.Location) bool {
	return predictions(rc).Fresh(l)
}

// PredictionsVouch reports whether this check's predictions can answer for l
// at all (wire.Predictions.Vouches): false when rc keeps none, or when l's
// directories disagree with what earlier tasks read of them.
func PredictionsVouch(rc RunbookContext, l wire.Location) bool {
	return predictions(rc).Vouches(l)
}

// PredictedPresentUnder reports whether an earlier task in this check
// predicted a path made inside the directory l (wire.Predictions.PresentUnder).
func PredictedPresentUnder(rc RunbookContext, l wire.Location) bool {
	return predictions(rc).PresentUnder(l)
}

// PredictedAbsentUnder reports whether an earlier task in this check
// predicted any path inside the directory l gone
// (wire.Predictions.AbsentUnder): when none did, no entry of l can be, so a
// method need not list them to ask.
func PredictedAbsentUnder(rc RunbookContext, l wire.Location) bool {
	return predictions(rc).AbsentUnder(l)
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
