// Package wire: predicted state across a check (Phase 46a). A check runs each
// task against the device as it really is, so a task whose outcome depends on
// what an earlier task in the same check only predicted would be answered
// wrongly: a directory a check of its undo would find empty only once the
// file inside was removed, which the check only predicted. The engine keeps,
// per device, the paths earlier tasks predicted absent or present, and hands
// a method this snapshot of them; on the Walk tier it rides ChildRequest to the
// per-task and session child.
package wire

import (
	"path"
	"strings"
)

// The two states a path can be predicted in.
const (
	// PredictedAbsent is a path an earlier task's check predicted gone.
	PredictedAbsent = "absent"
	// PredictedPresent is a path an earlier task's check predicted there.
	PredictedPresent = "present"
)

// Bounds on a snapshot, so a check that predicts a great many paths never
// grows a request without limit: past them the engine marks the device's
// ledger unknown rather than send part of it.
const (
	// MaxPredictedPaths bounds how many paths one device's snapshot holds.
	MaxPredictedPaths = 4096
	// MaxPredictedPathBytes bounds one path's length.
	MaxPredictedPathBytes = 4096
)

// PredictedPath is one path an earlier task's check predicted, and how.
type PredictedPath struct {
	// Path is absolute and cleaned (path.Clean), as the method recorded it.
	Path string `json:"path"`
	// State is PredictedAbsent or PredictedPresent.
	State string `json:"state"`
}

// Predictions is one device's snapshot of what earlier tasks in a check
// predicted. A nil snapshot, and one marked Unknown, answer nothing: the
// method reads the device as it is, as before predictions existed.
type Predictions struct {
	// Unknown says an earlier task's outcome could not be predicted (it
	// failed, was unchecked, or changed what nothing recorded), so no path
	// on the device can be vouched for.
	Unknown bool `json:"unknown,omitempty"`
	// Paths are the predicted paths, each at most once.
	Paths []PredictedPath `json:"paths,omitempty"`
}

// ValidPredictedPath reports whether p is a path a prediction may name:
// absolute, already cleaned, within the length bound, with no NUL, and in one
// of the two states.
func ValidPredictedPath(p PredictedPath) bool {
	switch {
	case p.State != PredictedAbsent && p.State != PredictedPresent:
		return false
	case len(p.Path) == 0 || len(p.Path) > MaxPredictedPathBytes || strings.ContainsRune(p.Path, 0):
		return false
	case !path.IsAbs(p.Path) || path.Clean(p.Path) != p.Path:
		return false
	}
	return true
}

// Absent reports whether the snapshot vouches that target will be gone by
// the time the asking task runs: recorded absent itself, or under a directory
// recorded absent with nothing under that directory recorded present again on
// the way down. Anything else (an unknown snapshot, no record, a present one,
// a path not absolute and cleaned) answers false, so the method reads the
// device as it is.
func (p *Predictions) Absent(target string) bool {
	if p == nil || p.Unknown || !path.IsAbs(target) || path.Clean(target) != target {
		return false
	}
	states := make(map[string]string, len(p.Paths))
	for _, pp := range p.Paths {
		states[pp.Path] = pp.State
	}
	// The nearest record on the way up decides: the path itself, then each
	// directory above it.
	for at := target; ; at = path.Dir(at) {
		if state, ok := states[at]; ok {
			return state == PredictedAbsent
		}
		if at == "/" {
			return false
		}
	}
}

// AbsentUnder reports whether the snapshot records any path strictly inside
// the directory dir as absent: without one, no entry of dir can be vouched
// gone, so a caller asking that of each entry need not list them. A nil or
// unknown snapshot answers false.
func (p *Predictions) AbsentUnder(dir string) bool {
	return p.anyUnder(dir, PredictedAbsent)
}

// PresentUnder reports whether the snapshot records a path inside the
// directory dir as present: an earlier task predicted it made there, so dir
// will not be empty whatever the device holds now. The engine drops every
// record beneath a directory recorded absent, so a present record that
// remains was made after any removal above it. A nil or unknown snapshot
// answers false.
func (p *Predictions) PresentUnder(dir string) bool {
	return p.anyUnder(dir, PredictedPresent)
}

// anyUnder reports whether the snapshot records a path strictly inside dir
// in state.
func (p *Predictions) anyUnder(dir, state string) bool {
	if p == nil || p.Unknown {
		return false
	}
	prefix := strings.TrimSuffix(dir, "/") + "/"
	for _, pp := range p.Paths {
		if pp.State == state && strings.HasPrefix(pp.Path, prefix) {
			return true
		}
	}
	return false
}
