// Tests of the check's prediction ledger (predicted.go): what a level's
// results add to it, and which results drop a device's predictions.
package engine

import (
	"context"
	"errors"
	"hash/fnv"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ledgerRun is a run holding only what recordPredictions reads: a ledger and
// a DAG whose nodes name a built-in method declaring PredictsPaths ("n"), one
// that does not ("plain"), an external one declaring it ("ext"), and an
// in-process engine action ("fact").
func ledgerRun(t *testing.T) *run {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	invoke := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	for _, d := range []collection.Descriptor{
		{Name: "enginetest.predicts", Manifest: collection.Manifest{PredictsPaths: true}},
		{Name: "enginetest.plain", Manifest: collection.Manifest{}},
		{Name: "enginetest.external", Manifest: collection.Manifest{PredictsPaths: true}, Provider: &collection.Provider{}},
	} {
		d.Invoke, d.Check = invoke, invoke
		d.Manifest.Status, d.Manifest.SupportsCheck = collection.StatusImplemented, true
		d.Manifest.Reversibility = collection.Reversibility{Notes: "a test fixture"}
		if err := collection.Register(d); err != nil {
			t.Fatalf("registering %s: %v", d.Name, err)
		}
	}
	return &run{predicted: newPredictionLedger(), dag: &DAG{Nodes: map[string]*Task{
		"n":     {FQCN: "enginetest.predicts"},
		"plain": {FQCN: "enginetest.plain"},
		"ext":   {FQCN: "enginetest.external"},
		"fact":  {FQCN: "set_fact"},
	}}}
}

// dirsOf are the identities along p: each directory above it numbered by
// a hash of its path, so every path names its directories alike.
func dirsOf(p string) []wire.DirID {
	var out []wire.DirID
	for at := p; ; {
		at = parentOf(at)
		h := fnv.New32a()
		_, _ = h.Write([]byte(at))
		out = append(out, wire.DirID{Path: at, ID: "1:" + strconv.FormatUint(uint64(h.Sum32()), 10)})
		if at == "/" {
			return out
		}
	}
}

// parentOf is path.Dir for an absolute path.
func parentOf(p string) string {
	for i := len(p) - 1; i > 0; i-- {
		if p[i] == '/' {
			return p[:i]
		}
	}
	return "/"
}

func absent(p string) wire.PredictedPath {
	return wire.PredictedPath{Path: p, State: wire.PredictedAbsent, Dirs: dirsOf(p)}
}

func present(p string) wire.PredictedPath {
	return wire.PredictedPath{Path: p, State: wire.PredictedPresent, Dirs: dirsOf(p)}
}

// predictedOn is a checked result of node "n" on device recording paths.
func predictedOn(device string, paths ...wire.PredictedPath) NodeResult {
	record := make([]any, 0, len(paths))
	for _, p := range paths {
		dirs := make([]any, 0, len(p.Dirs))
		for _, d := range p.Dirs {
			dirs = append(dirs, map[string]any{"path": d.Path, "id": d.ID})
		}
		record = append(record, map[string]any{"path": p.Path, "state": p.State, "dirs": dirs})
	}
	return NodeResult{NodeID: "n", Device: device, Checked: true, Changed: true, Stats: map[string]any{sdk.StatPredictedPaths: record}}
}

func predicted(paths ...wire.PredictedPath) NodeResult { return predictedOn("dev-1", paths...) }

func loc(p string) wire.Location { return wire.Location{Path: p, Dirs: dirsOf(p)} }

func TestLedger_RecordsAcrossLevelsPerDevice(t *testing.T) {
	r := ledgerRun(t)
	if snap := r.predicted.snapshot("dev-1"); snap == nil || len(snap.Paths) != 0 {
		t.Fatalf("a device nothing recorded on: %+v, want an empty snapshot", snap)
	}
	r.recordPredictions([]NodeResult{predicted(present("/d/a"), present("/d/b"), present("/e"))})
	r.recordPredictions([]NodeResult{predicted(absent("/d"))})
	snap := r.predicted.snapshot("dev-1")
	if len(snap.Paths) != 2 || snap.Paths[0].Path != "/d" || snap.Paths[1].Path != "/e" {
		t.Fatalf("snapshot = %+v, want /d absent and /e present", snap.Paths)
	}
	if !snap.Absent(loc("/d/a")) || snap.PresentUnder(loc("/d")) {
		t.Errorf("a record under the removed /d survived: %+v", snap.Paths)
	}
	if len(r.predicted.snapshot("dev-2").Paths) != 0 {
		t.Error("a prediction on dev-1 reached dev-2")
	}
	var none *predictionLedger
	none.dropAll()
	if none.snapshot("dev-1") != nil {
		t.Error("a run keeping no ledger handed out a snapshot")
	}
}

// TestLedger_AnythingButARecordingCheckDropsTheDevice: each result that is
// not a successful check by a built-in method recording its paths may stand
// for a change nothing recorded, whatever it reported, so the device is read
// as it is again; a later record starts it over.
func TestLedger_AnythingButARecordingCheckDropsTheDevice(t *testing.T) {
	for name, nr := range map[string]NodeResult{
		"failed":                       {NodeID: "n", Device: "dev-1", Checked: true, Err: errors.New("refused")},
		"unchecked":                    {NodeID: "n", Device: "dev-1", Unchecked: true},
		"skipped":                      {NodeID: "n", Device: "dev-1", Checked: true, Skipped: true},
		"a real task":                  {NodeID: "n", Device: "dev-1"},
		"unparsed":                     {NodeID: "n", Device: "dev-1", Checked: true, Stats: map[string]any{sdk.StatPredictedPaths: "unknown"}},
		"undeclared, no change":        {NodeID: "plain", Device: "dev-1", Checked: true},
		"undeclared change":            {NodeID: "plain", Device: "dev-1", Checked: true, Changed: true},
		"external, declaring":          {NodeID: "ext", Device: "dev-1", Checked: true, Changed: true},
		"a node the DAG does not hold": {NodeID: "gone", Device: "dev-1", Checked: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predicted(absent("/f"))})
			r.recordPredictions([]NodeResult{nr})
			if snap := r.predicted.snapshot("dev-1"); len(snap.Paths) != 0 || len(snap.Dirs) != 0 {
				t.Fatalf("snapshot = %+v, want it dropped", snap)
			}
			r.recordPredictions([]NodeResult{predicted(absent("/g"))})
			if snap := r.predicted.snapshot("dev-1"); snap.Absent(loc("/f")) || !snap.Absent(loc("/g")) {
				t.Errorf("after a later record: %+v, want only /g", snap.Paths)
			}
		})
	}
}

func TestLedger_KeepsThroughWhatTouchesNoDevice(t *testing.T) {
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(absent("/f"))})
	r.recordPredictions([]NodeResult{
		{NodeID: "fact", Device: "dev-1", Checked: true},
		{NodeID: "fact", Checked: true},
		{NodeID: "n", Device: "dev-1", Checked: true},
	})
	if !r.predicted.snapshot("dev-1").Absent(loc("/f")) {
		t.Error("an engine action that touches no device, or a recording method with nothing to record, dropped the device")
	}
}

// TestLedger_ADevicelessTaskDropsEveryDevice: a task with no device, or a skip
// decided for a whole node, may stand for a change on any device.
func TestLedger_ADevicelessTaskDropsEveryDevice(t *testing.T) {
	for name, nr := range map[string]NodeResult{
		"a node-level skip":      {NodeID: "n", Skipped: true},
		"a deviceless task":      {NodeID: "plain", Checked: true},
		"a failed engine action": {NodeID: "fact", Checked: true, Err: errors.New("assert")},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predictedOn("dev-1", absent("/f")), predictedOn("dev-2", absent("/f"))})
			r.recordPredictions([]NodeResult{nr, predictedOn("dev-3", absent("/f"))})
			for _, d := range []string{"dev-1", "dev-2", "dev-3"} {
				if r.predicted.snapshot(d).Absent(loc("/f")) {
					t.Errorf("%s kept its predictions", d)
				}
			}
		})
	}
}

// TestLedger_SiblingsThatDisagreeLeaveTheDeviceDropped: two nodes of one level
// ran in no order the ledger knows, so a path they predict differently is
// neither, and a sibling that drops the device takes the level's records.
func TestLedger_SiblingsThatDisagreeLeaveTheDeviceDropped(t *testing.T) {
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(absent("/f")), predicted(present("/f"))})
	if len(r.predicted.snapshot("dev-1").Paths) != 0 {
		t.Error("siblings that disagree were recorded")
	}
	r.recordPredictions([]NodeResult{predicted(absent("/f")), {NodeID: "plain", Device: "dev-1", Checked: true}})
	if len(r.predicted.snapshot("dev-1").Paths) != 0 {
		t.Error("a sibling that drops the device left the level's records")
	}
	r.recordPredictions([]NodeResult{predicted(absent("/f")), predicted(absent("/f"), absent("/g"))})
	if snap := r.predicted.snapshot("dev-1"); !snap.Absent(loc("/g")) {
		t.Errorf("siblings that agree: %+v", snap.Paths)
	}
}

// TestLedger_ADirectoryUnderTwoPathsDropsTheDevice: one identity met under a
// second path is a bind mount or a folded name, and one path met with a
// second identity was replaced, so nothing on the device can be vouched for.
func TestLedger_ADirectoryUnderTwoPathsDropsTheDevice(t *testing.T) {
	for name, second := range map[string]wire.PredictedPath{
		"an identity under a second path": {Path: "/mnt/x", State: wire.PredictedPresent, Dirs: []wire.DirID{{Path: "/mnt", ID: dirsOf("/d/f")[0].ID}}},
		"a path with a second identity":   {Path: "/d/y", State: wire.PredictedPresent, Dirs: []wire.DirID{{Path: "/d", ID: "9:9"}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predicted(absent("/d/f"))})
			r.recordPredictions([]NodeResult{predicted(second)})
			if snap := r.predicted.snapshot("dev-1"); len(snap.Paths) != 0 {
				t.Errorf("snapshot = %+v, want it dropped", snap.Paths)
			}
		})
	}
}

func TestLedger_PastItsBoundIsDropped(t *testing.T) {
	r := ledgerRun(t)
	paths := make([]wire.PredictedPath, 0, wire.MaxPredictedPaths+1)
	for i := range wire.MaxPredictedPaths + 1 {
		paths = append(paths, absent("/p/"+strconv.Itoa(i)))
	}
	r.recordPredictions([]NodeResult{predicted(paths...)})
	if snap := r.predicted.snapshot("dev-1"); len(snap.Paths) != 0 {
		t.Errorf("a ledger past its bound kept %d paths", len(snap.Paths))
	}
}

// TestLedger_APathMadeVouchesForNothingRemovedBesideIt: a path made in a
// directory may be another spelling of a name removed there (a filesystem
// that folds case) or a link to where something removed still is, so every
// absent record in that directory is read from the device again; paths made
// there before stay made.
func TestLedger_APathMadeVouchesForNothingRemovedBesideIt(t *testing.T) {
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(present("/a/kept"))})
	r.recordPredictions([]NodeResult{predicted(absent("/a/foo"), absent("/a/dir/x"), absent("/b/other"))})
	r.recordPredictions([]NodeResult{predicted(present("/a/FOO"))})
	snap := r.predicted.snapshot("dev-1")
	if snap.Absent(loc("/a/foo")) || snap.Absent(loc("/a/dir/x")) {
		t.Errorf("a name removed beside a path made is still vouched gone: %+v", snap.Paths)
	}
	if !snap.Absent(loc("/b/other")) || !snap.PresentUnder(loc("/a")) {
		t.Errorf("records elsewhere were dropped: %+v", snap.Paths)
	}
}

// TestLedger_APathMadeBringsItsParentsBack: a directory made with its missing
// parents (file.directory, as mkdir -p) means each parent is there again.
func TestLedger_APathMadeBringsItsParentsBack(t *testing.T) {
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(absent("/a"))})
	r.recordPredictions([]NodeResult{predicted(present("/a/b/c"))})
	snap := r.predicted.snapshot("dev-1")
	if snap.Absent(loc("/a")) || snap.Absent(loc("/a/b")) {
		t.Errorf("a parent of a path made is vouched gone: %+v", snap.Paths)
	}
}
