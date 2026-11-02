// Package engine: the ledger of what a check predicted, per device (Phase
// 46a, pkg/wire's predicted.go).
//
// A check runs each task against the device as it is, so a task whose
// outcome depends on what an earlier task only predicted would be answered
// wrongly. The ledger keeps, per device, the paths earlier tasks' checks
// recorded (sdk.RecordPredicted), and each later task's method gets the
// device's snapshot (sdk.PredictedState), in process or on the child request.
//
// Its one rule is that a prediction may only bring a check's answer closer to
// the real run's, never vouch for a path the real run meets differently, and
// every choice below follows from it:
//
//   - It is kept only when the whole run is a check. In a real run with a few
//     check_mode tasks, the real tasks between them change the device.
//   - A device keeps its predictions only while every task on it is a check
//     that succeeded, by a built-in method declaring that its check records
//     every path it changes (Manifest.PredictsPaths), or an in-process engine
//     action that touches no device. Anything else drops them, whatever it
//     reported: a method that does not record its paths may have changed one
//     even when it said it changed nothing (its answer was read against the
//     device as it is), a skipped task may run in the real run, a failure ends
//     what can be predicted, and an external program's records are not
//     trusted. A task with no device, and a result that reaches the run
//     outside a level, drop every device's.
//   - Dropping returns the device to what checks answered before predictions
//     existed: the device as it is. A later record starts the device's
//     predictions again, which is sound, since each record is what its own
//     task does, later than everything dropped.
//   - It is written only at the level barrier, after every node of a level has
//     returned and on one goroutine, so a sibling in the same level (a
//     parallel group's other branch) never reads another's prediction as
//     earlier; siblings that disagree on a path drop the device's.
//   - Paths are physical paths with the identities of their directories
//     (wire.Location), and one identity met under two paths (a bind mount, a
//     name a case-insensitive filesystem folds) drops the device's.
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

// deviceLedger is one device's predicted paths and the directory identities
// its tasks read.
type deviceLedger struct {
	paths map[string]string // physical path -> state
	ids   map[string]string // "dev:ino" -> physical path
	dirs  map[string]string // physical path -> "dev:ino"
}

// newDeviceLedger returns an empty ledger.
func newDeviceLedger() *deviceLedger {
	return &deviceLedger{paths: map[string]string{}, ids: map[string]string{}, dirs: map[string]string{}}
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

// snapshot returns device's predictions for a method to read: empty, never
// nil, when nothing is recorded, since a ledger exists only in a check and a
// method records its own paths exactly when it is handed a snapshot. A nil
// ledger (a real run) has none.
func (l *predictionLedger) snapshot(device string) *wire.Predictions {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := &wire.Predictions{}
	d := l.devices[device]
	if d == nil {
		return out
	}
	for _, p := range slices.Sorted(maps.Keys(d.paths)) {
		out.Paths = append(out.Paths, wire.PredictedPath{Path: p, State: d.paths[p]})
	}
	for _, p := range slices.Sorted(maps.Keys(d.dirs)) {
		out.Dirs = append(out.Dirs, wire.DirID{Path: p, ID: d.dirs[p]})
	}
	return out
}

// dropAll drops every device's predictions.
func (l *predictionLedger) dropAll() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.devices)
}

// levelRecords is what one level predicts for one device, kept apart until
// the level is read whole.
type levelRecords struct {
	drop  bool
	paths []wire.PredictedPath
}

// recordPredictions folds one level's results into the ledger, after every
// node of the level returned (Run's level barrier). See the package comment
// for which results keep a device's predictions and which drop them.
func (r *run) recordPredictions(results []NodeResult) {
	if r.predicted == nil {
		return
	}
	level := map[string]*levelRecords{}
	device := func(id string) *levelRecords {
		if level[id] == nil {
			level[id] = &levelRecords{}
		}
		return level[id]
	}
	for _, nr := range results {
		if nr.Device == "" {
			// A task with no device, or a skip decided for a whole node, may
			// stand for a change on any device.
			if !r.touchesNoDevice(nr) {
				r.predicted.dropAll()
				return
			}
			continue
		}
		rec := device(nr.Device)
		paths, ok := r.predictionsOf(nr)
		if !ok {
			rec.drop = true
			continue
		}
		rec.paths = append(rec.paths, paths...)
	}
	l := r.predicted
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, rec := range level {
		if rec.drop || !conflictFree(rec.paths) {
			delete(l.devices, id)
			continue
		}
		if len(rec.paths) == 0 {
			continue
		}
		d := l.devices[id]
		if d == nil {
			d = newDeviceLedger()
			l.devices[id] = d
		}
		if !d.apply(rec.paths) {
			delete(l.devices, id)
		}
	}
}

// predictionsOf returns what result nr adds to its device's predictions, and
// false when it drops them instead.
func (r *run) predictionsOf(nr NodeResult) ([]wire.PredictedPath, bool) {
	if nr.Err != nil || nr.Skipped || nr.Unchecked || !nr.Checked {
		return nil, false
	}
	if r.touchesNoDevice(nr) {
		return nil, true
	}
	if !r.predictsPaths(nr.NodeID) {
		return nil, false
	}
	v, ok := nr.Stats[sdk.StatPredictedPaths]
	if !ok {
		// A declaring method that records nothing changes nothing: its
		// declaration is that it records every path it changes.
		return nil, true
	}
	paths, err := sdk.ParsePredicted(v)
	if err != nil {
		return nil, false
	}
	return paths, true
}

// touchesNoDevice reports whether nr is a checked result of an in-process
// engine action that changes nothing on any device (builtinCheckable): it
// neither changes a path nor stands in for a task that would.
func (r *run) touchesNoDevice(nr NodeResult) bool {
	task := r.dag.Nodes[BaseNodeID(nr.NodeID)]
	return task != nil && nr.Checked && !nr.Skipped && nr.Err == nil && builtinCheckable[task.FQCN]
}

// conflictFree reports whether one level's records agree: siblings ran in no
// order the ledger knows, so a path they predict differently is neither.
func conflictFree(paths []wire.PredictedPath) bool {
	seen := map[string]string{}
	for _, p := range paths {
		if prior, ok := seen[p.Path]; ok && prior != p.State {
			return false
		}
		seen[p.Path] = p.State
	}
	return true
}

// apply adds records to d in order, and reports false when the device's
// predictions must be dropped instead: a directory identity met under a
// second path, or a ledger past its bound.
func (d *deviceLedger) apply(records []wire.PredictedPath) bool {
	for _, p := range records {
		for _, dir := range p.Dirs {
			if at, ok := d.ids[dir.ID]; ok && at != dir.Path {
				return false
			}
			if id, ok := d.dirs[dir.Path]; ok && id != dir.ID {
				return false
			}
			d.ids[dir.ID], d.dirs[dir.Path] = dir.Path, dir.ID
		}
		d.set(p.Path, p.State)
	}
	return len(d.paths) <= wire.MaxPredictedPaths && len(d.dirs) <= wire.MaxPredictedPaths
}

// set records p's predicted state, keeping every other record one that errs
// toward reading the device as it is rather than vouching for a path:
//
//   - A path predicted gone takes everything under it with it, so every
//     record beneath it goes; a later present record beneath it is written by
//     the task that makes it.
//   - A path predicted there drops every absent record in the directory that
//     holds it, its own and its siblings' and theirs beneath them: what p now
//     is may be another spelling of a name removed there (a filesystem that
//     folds case), or a link to where something removed still is. Present
//     records stay, since a task already made them.
//   - A path predicted there has every directory above it there too (a
//     method such as file.directory makes the missing ones), so an ancestor
//     recorded gone is recorded present instead.
func (d *deviceLedger) set(p, state string) {
	under := p
	if state == wire.PredictedPresent {
		under = path.Dir(p)
	}
	prefix := strings.TrimSuffix(under, "/") + "/"
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

// predictsPaths reports whether node nodeID's method is built in and declares
// that its check records every path it changes
// (collection.Manifest.PredictsPaths). An external program's declaration is
// not trusted: its records would change what a built-in method's check
// answers.
func (r *run) predictsPaths(nodeID string) bool {
	task := r.dag.Nodes[BaseNodeID(nodeID)]
	if task == nil {
		return false
	}
	desc, ok := collection.Lookup(task.FQCN)
	return ok && desc.Provider == nil && desc.Manifest.PredictsPaths
}
