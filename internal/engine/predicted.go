// Package engine: the ledger of what a check predicted removed, per device
// (Phase 46a, pkg/wire's predicted.go).
//
// A check runs each task against the device as it is, so a task whose
// outcome depends on what an earlier task only predicted would be answered
// wrongly: a rollback that removes a copied file and then the directory it
// was copied into is refused at the directory, which the check finds still
// holding the file. The ledger keeps, per device, the paths earlier tasks'
// checks recorded removed (sdk.RecordPredicted), and each later task's method
// gets the device's snapshot (sdk.PredictedState), in process or on the child
// request.
//
// Its one rule is that a prediction may only bring a check's answer closer to
// the real run's, never vouch for a path the real run meets differently, and
// it keeps the rule by vouching for one kind of claim only, under one
// condition:
//
//   - The only claim is that a path is gone. Removing more can never make one
//     false, and a second spelling of a path (a link, a bind mount, a name a
//     filesystem folds, another device sharing the filesystem) can only miss
//     a record, which reads the device as it is.
//   - Only making something can make one false, so the ledger is kept only
//     while every task in the run, on every device, is a successful check by
//     a built-in method declaring that its check records every path it
//     changes and making nothing (Manifest.PredictsPaths: file.remove), that
//     recorded, or an in-process engine action that touches no device. The
//     first anything else (another method's task whatever it reported, a
//     skipped, failed or unchecked task, a task with no device, a pause that
//     hands the device to a person, a result reaching the run outside a level,
//     a task allowed to fail) drops every device's predictions for the rest of
//     the run.
//   - A claim rests on the real run doing the removal its check predicted, and
//     a check cannot see every refusal coming, so file.remove records one only
//     when the directory holding the path allows it (pkg/remotefile's
//     Removable) and it is not a whole tree. What refuses a removal only when
//     it is tried (an immutable flag, a busy mount point) is the one thing left
//     that a later task can inherit: the same thing a check of that removal
//     alone already could not see.
//   - It is kept only when the whole run is a check. In a real run with a few
//     check_mode tasks, the real tasks between them change the device.
//   - It is written only at the level barrier, after every node of a level has
//     returned and on one goroutine, so a sibling in the same level never
//     reads another's prediction as earlier. Removals in either order leave
//     the same paths gone.
package engine

import (
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// deviceLedger is one device's paths predicted removed and the directory
// identities its tasks read.
type deviceLedger struct {
	paths map[string]bool   // physical path recorded removed
	ids   map[string]string // "dev:ino" -> physical path
	dirs  map[string]string // physical path -> "dev:ino"
}

// newDeviceLedger returns an empty ledger.
func newDeviceLedger() *deviceLedger {
	return &deviceLedger{paths: map[string]bool{}, ids: map[string]string{}, dirs: map[string]string{}}
}

// predictionLedger is a run's ledgers by device id, or dropped for the rest
// of the run.
type predictionLedger struct {
	mu      sync.Mutex
	dropped bool
	devices map[string]*deviceLedger
}

// newPredictionLedger returns an empty ledger.
func newPredictionLedger() *predictionLedger {
	return &predictionLedger{devices: map[string]*deviceLedger{}}
}

// snapshot returns device's predictions for a method to read: empty, never
// nil, when nothing is recorded yet, since a method records its own paths
// exactly when it is handed a snapshot; nil when the run keeps none (a real
// run) or they were dropped, so its methods read the device as it is and
// record nothing.
func (l *predictionLedger) snapshot(device string) *wire.Predictions {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dropped {
		return nil
	}
	out := &wire.Predictions{}
	d := l.devices[device]
	if d == nil {
		return out
	}
	for _, p := range slices.Sorted(maps.Keys(d.paths)) {
		out.Paths = append(out.Paths, wire.PredictedPath{Path: p, State: wire.PredictedAbsent})
	}
	for _, p := range slices.Sorted(maps.Keys(d.dirs)) {
		out.Dirs = append(out.Dirs, wire.DirID{Path: p, ID: d.dirs[p]})
	}
	return out
}

// dropAll drops every device's predictions for the rest of the run.
func (l *predictionLedger) dropAll() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropLocked()
}

// dropLocked is dropAll with l.mu held.
func (l *predictionLedger) dropLocked() {
	l.dropped = true
	clear(l.devices)
}

// recordPredictions folds one level's results into the ledger, after every
// node of the level returned (Run's level barrier). See the package comment
// for which results keep the ledger and which drop it.
func (r *run) recordPredictions(results []NodeResult) {
	if r.predicted == nil {
		return
	}
	level := map[string][]wire.PredictedPath{}
	for _, nr := range results {
		if r.touchesNoDevice(nr) {
			continue
		}
		paths, ok := r.predictionsOf(nr)
		if !ok {
			r.predicted.dropAll()
			return
		}
		level[nr.Device] = append(level[nr.Device], paths...)
	}
	l := r.predicted
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dropped {
		return
	}
	for _, id := range slices.Sorted(maps.Keys(level)) {
		if len(level[id]) == 0 {
			continue
		}
		d := l.devices[id]
		if d == nil {
			d = newDeviceLedger()
			l.devices[id] = d
		}
		if !d.apply(level[id]) {
			l.dropLocked()
			return
		}
	}
}

// predictionsOf returns what result nr adds to its device's predictions, and
// false when it drops the run's instead.
func (r *run) predictionsOf(nr NodeResult) ([]wire.PredictedPath, bool) {
	if nr.Device == "" || nr.Err != nil || nr.Skipped || nr.Unchecked || !nr.Checked {
		return nil, false
	}
	// A task its author lets fail (ignore_errors) is one whose real run may
	// not do what its check predicted, and the run goes on past it.
	if task := r.dag.Nodes[BaseNodeID(nr.NodeID)]; task == nil || task.IgnoreErrors {
		return nil, false
	}
	// A declaring method records on every check it is handed a snapshot for,
	// an empty list when it changes nothing; a check that recorded nothing was
	// handed none, or did not keep its declaration, and either way what it did
	// is unknown.
	if !r.predictsPaths(nr.NodeID) || !nr.predictedSet {
		return nil, false
	}
	paths, err := sdk.ParsePredicted(nr.predicted)
	if err != nil {
		return nil, false
	}
	return paths, true
}

// touchesNoDevice reports whether nr is a checked result of an in-process
// engine action that changes nothing on any device (builtinCheckable), with a
// device or none. A pause is not one: it hands the device to a person, who
// may change it.
func (r *run) touchesNoDevice(nr NodeResult) bool {
	task := r.dag.Nodes[BaseNodeID(nr.NodeID)]
	if task == nil || !nr.Checked || nr.Skipped || nr.Err != nil || !builtinCheckable[task.FQCN] {
		return false
	}
	return task.FQCN != "pause" && task.FQCN != "pleiades.builtin.pause"
}

// apply adds records to d, and reports false when the run's predictions must
// be dropped instead: a record that is not a removal, a directory identity met
// under a second path (a bind mount, which a removal cannot make wrong but
// which nothing here models), or a ledger past its bound.
func (d *deviceLedger) apply(records []wire.PredictedPath) bool {
	for _, p := range records {
		if p.State != wire.PredictedAbsent {
			return false
		}
		for _, dir := range p.Dirs {
			if at, ok := d.ids[dir.ID]; ok && at != dir.Path {
				return false
			}
			if id, ok := d.dirs[dir.Path]; ok && id != dir.ID {
				return false
			}
			d.ids[dir.ID], d.dirs[dir.Path] = dir.Path, dir.ID
		}
		// A path removed takes everything under it with it.
		prefix := strings.TrimSuffix(p.Path, "/") + "/"
		for existing := range d.paths {
			if strings.HasPrefix(existing, prefix) {
				delete(d.paths, existing)
			}
		}
		d.paths[p.Path] = true
	}
	return len(d.paths) <= wire.MaxPredictedPaths && len(d.dirs) <= wire.MaxPredictedPaths
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
