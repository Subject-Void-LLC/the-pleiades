// Package engine: the ledger of what a check predicted, per device (Phase
// 46a, pkg/wire's predicted.go).
//
// A check runs each task against the device as it is, so a task whose
// outcome depends on what an earlier task only predicted would be answered
// wrongly. The ledger keeps, per device, the paths earlier tasks' checks
// recorded (sdk.RecordPredicted), and each later task's method gets the
// device's snapshot (sdk.PredictedState), in process or on the child request.
//
// It is written only at the level barrier, after every node of a level has
// returned and on one goroutine, so a sibling in the same level (a parallel
// group's other branch) never reads another's prediction as earlier. A device
// whose outcome stopped being predictable becomes unknown for the rest of the
// check, which answers every question as the device is now: a task on it that
// failed, could not be checked, or predicted a change without its method
// declaring that it records every path it changes (Manifest.PredictsPaths), and
// a ledger past its bound. Unknown never turns back.
package engine

import (
	"maps"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// deviceLedger is one device's predicted paths, or unknown.
type deviceLedger struct {
	unknown bool
	paths   map[string]string
}

// predictionLedger is a run's ledgers by device id.
type predictionLedger struct {
	mu      sync.Mutex
	devices map[string]*deviceLedger
}

// newPredictionLedger returns an empty ledger.
func newPredictionLedger() *predictionLedger {
	return &predictionLedger{devices: map[string]*deviceLedger{}}
}

// snapshot returns device's predictions for a method to read, nil when no
// task has recorded anything on it yet.
func (l *predictionLedger) snapshot(device string) *wire.Predictions {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.devices[device]
	if d == nil {
		return nil
	}
	if d.unknown {
		return &wire.Predictions{Unknown: true}
	}
	out := &wire.Predictions{Paths: make([]wire.PredictedPath, 0, len(d.paths))}
	for _, p := range slices.Sorted(maps.Keys(d.paths)) {
		out.Paths = append(out.Paths, wire.PredictedPath{Path: p, State: d.paths[p]})
	}
	return out
}

// device returns device's ledger, made on first use. l.mu is held.
func (l *predictionLedger) device(id string) *deviceLedger {
	d := l.devices[id]
	if d == nil {
		d = &deviceLedger{paths: map[string]string{}}
		l.devices[id] = d
	}
	return d
}

// recordPredictions folds one level's check results into the ledger, after
// every node of the level returned (Run's level barrier). Only results of
// checked tasks count; a real task in a run with check_mode tasks predicts
// nothing.
func (r *run) recordPredictions(results []NodeResult) {
	if r.predicted == nil {
		return
	}
	l := r.predicted
	l.mu.Lock()
	defer l.mu.Unlock()
	// What this level predicts, per device, kept apart until the level is
	// read whole, so two siblings predicting one path differently are seen.
	level := map[string]map[string]string{}
	for _, nr := range results {
		if nr.Device == "" || (!nr.Checked && !nr.Unchecked) {
			continue
		}
		d := l.device(nr.Device)
		if d.unknown {
			continue
		}
		switch {
		case nr.Unchecked, nr.Err != nil:
			d.unknown = true
			continue
		case nr.Skipped:
			continue
		}
		var paths []wire.PredictedPath
		if v, ok := nr.Stats[sdk.StatPredictedPaths]; ok {
			parsed, err := sdk.ParsePredicted(v)
			if err != nil {
				d.unknown = true
				continue
			}
			paths = parsed
		}
		// A change from a method that does not record every path it changes
		// may have changed one nothing recorded.
		if nr.Changed && !r.predictsPaths(nr.NodeID) {
			d.unknown = true
			continue
		}
		seen := level[nr.Device]
		if seen == nil {
			seen = map[string]string{}
			level[nr.Device] = seen
		}
		for _, p := range paths {
			if prior, ok := seen[p.Path]; ok && prior != p.State {
				d.unknown = true
			}
			seen[p.Path] = p.State
		}
	}
	for device, paths := range level {
		d := l.device(device)
		if d.unknown {
			continue
		}
		for _, p := range slices.Sorted(maps.Keys(paths)) {
			d.set(p, paths[p])
		}
		if len(d.paths) > wire.MaxPredictedPaths {
			d.unknown, d.paths = true, nil
		}
	}
}

// set records p's predicted state, keeping every other record one that errs
// toward reading the device as it is rather than vouching for a path:
//
//   - A path predicted gone takes everything under it with it, so every
//     record beneath it goes; a later present record beneath it is written by
//     the task that makes it.
//   - A path predicted there drops the absent records beneath it: what p now
//     is (a directory made again, a link put where a directory was) may hold
//     what was removed before, so those paths are read from the device again.
//     Present records beneath it stay, since a task already made them.
//   - A path predicted there has every directory above it there too (a
//     method such as file.directory makes the missing ones), so an ancestor
//     recorded gone is recorded present instead. Its other absent records
//     beneath stay, which is still true: making /a/b again does not bring
//     back /a/old.
func (d *deviceLedger) set(p, state string) {
	p = path.Clean(p)
	prefix := strings.TrimSuffix(p, "/") + "/"
	for existing, was := range d.paths {
		if strings.HasPrefix(existing, prefix) && (state == wire.PredictedAbsent || was == wire.PredictedAbsent) {
			delete(d.paths, existing)
		}
	}
	if state == wire.PredictedPresent {
		for at := path.Dir(p); ; at = path.Dir(at) {
			if d.paths[at] == wire.PredictedAbsent {
				d.paths[at] = wire.PredictedPresent
			}
			if at == "/" {
				break
			}
		}
	}
	d.paths[p] = state
}

// predictsPaths reports whether node nodeID's method declares that its check
// records every path it changes (collection.Manifest.PredictsPaths). An
// engine action, or a name the registry does not hold, does not.
func (r *run) predictsPaths(nodeID string) bool {
	task := r.dag.Nodes[BaseNodeID(nodeID)]
	if task == nil {
		return false
	}
	desc, ok := collection.Lookup(task.FQCN)
	return ok && desc.Manifest.PredictsPaths
}
