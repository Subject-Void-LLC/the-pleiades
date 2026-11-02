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
//     that succeeded and recorded its paths, by a built-in method declaring
//     that its check records every path it changes (Manifest.PredictsPaths),
//     or an in-process engine action that touches no device. Anything else
//     drops them for the rest of the run, whatever it reported: a method that
//     does not record its paths may have changed one even when it said it
//     changed nothing (its answer was read against the device as it is), a
//     skipped task may run in the real run, a failure ends what can be
//     predicted, and an external program's records are not trusted. A task
//     with no device, and a result that reaches the run outside a level, drop
//     every device's.
//   - A drop lasts for the rest of the run. A later task's path is located
//     against the device as it is, and what a dropped task changed (a link, a
//     directory) is exactly what that location cannot see.
//   - It is written only at the level barrier, after every node of a level has
//     returned and on one goroutine, so a sibling in the same level (a
//     parallel group's other branch) never reads another's prediction as
//     earlier; siblings whose records overlap (one path at or under the
//     other's) with different states drop the device.
//   - Paths are physical paths with the identities of their directories
//     (wire.Location), recorded only where the device resolves them exactly as
//     written (internal/catalog/file's locate). One identity met under two
//     paths (a bind mount), and two names in one directory that differ only in
//     case or are not ASCII (a filesystem that folds them may hold one file
//     under both), drop the device.
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
// its tasks read, or dropped.
type deviceLedger struct {
	dropped bool
	paths   map[string]string // physical path -> state
	ids     map[string]string // "dev:ino" -> physical path
	dirs    map[string]string // physical path -> "dev:ino"
}

// newDeviceLedger returns an empty ledger.
func newDeviceLedger() *deviceLedger {
	return &deviceLedger{paths: map[string]string{}, ids: map[string]string{}, dirs: map[string]string{}}
}

// predictionLedger is a run's ledgers by device id.
type predictionLedger struct {
	mu      sync.Mutex
	dropped bool // every device's, for the rest of the run
	devices map[string]*deviceLedger
}

// newPredictionLedger returns an empty ledger.
func newPredictionLedger() *predictionLedger {
	return &predictionLedger{devices: map[string]*deviceLedger{}}
}

// snapshot returns device's predictions for a method to read: empty, never
// nil, when nothing is recorded yet, since a method records its own paths
// exactly when it is handed a snapshot; nil when the run keeps none (a real
// run) or the device's were dropped, so its methods read the device as it is
// and record nothing.
func (l *predictionLedger) snapshot(device string) *wire.Predictions {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	d := l.devices[device]
	if l.dropped || d != nil && d.dropped {
		return nil
	}
	out := &wire.Predictions{}
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

// dropAll drops every device's predictions for the rest of the run.
func (l *predictionLedger) dropAll() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dropped = true
	clear(l.devices)
}

// drop drops device's predictions for the rest of the run. l.mu is held.
func (l *predictionLedger) drop(device string) {
	l.devices[device] = &deviceLedger{dropped: true}
}

// levelRecords is what one level predicts for one device, kept apart until
// the level is read whole: each result's records, separately, so siblings
// can be compared.
type levelRecords struct {
	drop    bool
	results [][]wire.PredictedPath
}

// recordPredictions folds one level's results into the ledger, after every
// node of the level returned (Run's level barrier). See the package comment
// for which results keep a device's predictions and which drop them.
func (r *run) recordPredictions(results []NodeResult) {
	if r.predicted == nil {
		return
	}
	level := map[string]*levelRecords{}
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
		rec := level[nr.Device]
		if rec == nil {
			rec = &levelRecords{}
			level[nr.Device] = rec
		}
		paths, ok := r.predictionsOf(nr)
		if !ok {
			rec.drop = true
			continue
		}
		if len(paths) > 0 {
			rec.results = append(rec.results, paths)
		}
	}
	l := r.predicted
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dropped {
		return
	}
	for _, id := range slices.Sorted(maps.Keys(level)) {
		rec := level[id]
		d := l.devices[id]
		if d != nil && d.dropped {
			continue
		}
		if rec.drop || !siblingsAgree(rec.results) {
			l.drop(id)
			continue
		}
		if len(rec.results) == 0 {
			continue
		}
		if d == nil {
			d = newDeviceLedger()
			l.devices[id] = d
		}
		if !d.apply(slices.Concat(rec.results...)) {
			l.drop(id)
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
// engine action that changes nothing on any device (builtinCheckable): it
// neither changes a path nor stands in for a task that would.
func (r *run) touchesNoDevice(nr NodeResult) bool {
	task := r.dag.Nodes[BaseNodeID(nr.NodeID)]
	return task != nil && nr.Checked && !nr.Skipped && nr.Err == nil && builtinCheckable[task.FQCN]
}

// siblingsAgree reports whether one level's results can be applied in any
// order: siblings ran in no order the ledger knows, so two whose records
// overlap (one path at or under the other's) with different states could
// leave either answer.
func siblingsAgree(results [][]wire.PredictedPath) bool {
	if len(results) < 2 {
		return true
	}
	type mark struct {
		state  string
		result int
	}
	seen := map[string]mark{}
	for i, paths := range results {
		for _, p := range paths {
			seen[p.Path] = mark{state: p.State, result: i}
		}
	}
	for i, paths := range results {
		for _, p := range paths {
			for at := p.Path; ; at = path.Dir(at) {
				if m, ok := seen[at]; ok && m.result != i && m.state != p.State {
					return false
				}
				if at == "/" {
					break
				}
			}
		}
	}
	return true
}

// apply adds records to d in order, and reports false when the device's
// predictions must be dropped instead: a directory identity met under a
// second path, a name that could fold into another, or a ledger past its
// bound.
func (d *deviceLedger) apply(records []wire.PredictedPath) bool {
	for _, p := range records {
		if p.State != wire.PredictedAbsent && p.State != wire.PredictedPresent {
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
		if !d.foldFree(p.Path) {
			return false
		}
		d.set(p.Path, p.State)
	}
	return len(d.paths) <= wire.MaxPredictedPaths && len(d.dirs) <= wire.MaxPredictedPaths
}

// foldFree reports whether p's last name is ASCII and differs from every
// other recorded name in its directory by more than case, so no filesystem
// that folds names (macOS's by default) could hold one file under both.
func (d *deviceLedger) foldFree(p string) bool {
	name, dir := path.Base(p), path.Dir(p)
	for i := range len(name) {
		if name[i] >= 0x80 {
			return false
		}
	}
	for existing := range d.paths {
		if existing != p && path.Dir(existing) == dir && strings.EqualFold(path.Base(existing), name) {
			return false
		}
	}
	return true
}

// nearest returns the state of the record nearest p on the way up.
func (d *deviceLedger) nearest(p string) (string, bool) {
	for at := p; ; at = path.Dir(at) {
		if state, ok := d.paths[at]; ok {
			return state, true
		}
		if at == "/" {
			return "", false
		}
	}
}

// set records p's predicted state:
//
//   - A path predicted gone takes everything under it with it, so every
//     record beneath it goes.
//   - A path predicted there where the real run will have nothing (the
//     nearest record at or above it says gone, or says a directory made
//     fresh) is made fresh (wire.PredictedFresh): it holds only what a later
//     record puts under it. Otherwise it is there as the device holds it.
//   - A path predicted there has every directory above it there too (a
//     method such as file.directory makes the missing ones), so an ancestor
//     recorded gone is made fresh.
func (d *deviceLedger) set(p, state string) {
	if state == wire.PredictedAbsent {
		prefix := strings.TrimSuffix(p, "/") + "/"
		for existing := range d.paths {
			if strings.HasPrefix(existing, prefix) {
				delete(d.paths, existing)
			}
		}
		d.paths[p] = state
		return
	}
	prior, known := d.nearest(p)
	if own, ok := d.paths[p]; ok && own != wire.PredictedAbsent {
		prior, known = own, true
		if own == wire.PredictedPresent {
			known = false // there as the device holds it, and still so
		}
	}
	for at := path.Dir(p); ; at = path.Dir(at) {
		if d.paths[at] == wire.PredictedAbsent {
			d.paths[at] = wire.PredictedFresh
		}
		if at == "/" {
			break
		}
	}
	if known && (prior == wire.PredictedAbsent || prior == wire.PredictedFresh) {
		d.paths[p] = wire.PredictedFresh
		return
	}
	d.paths[p] = wire.PredictedPresent
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
