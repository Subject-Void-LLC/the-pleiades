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
// that does not ("plain"), an external one declaring it ("ext"), and the
// in-process engine actions set_fact ("fact") and pause.
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
		"pause": {FQCN: "pause"},
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
	return NodeResult{NodeID: "n", Device: device, Checked: true, Changed: true, predicted: record, predictedSet: true}
}

func predicted(paths ...wire.PredictedPath) NodeResult { return predictedOn("dev-1", paths...) }

func loc(p string) wire.Location { return wire.Location{Path: p, Dirs: dirsOf(p)} }

func TestLedger_RecordsAcrossLevelsPerDevice(t *testing.T) {
	r := ledgerRun(t)
	if snap := r.predicted.snapshot("dev-1"); snap == nil || len(snap.Paths) != 0 {
		t.Fatalf("a device nothing recorded on: %+v, want an empty snapshot", snap)
	}
	r.recordPredictions([]NodeResult{predicted(absent("/d/a"), absent("/d/b"), absent("/e"))})
	r.recordPredictions([]NodeResult{predicted(absent("/d"))})
	snap := r.predicted.snapshot("dev-1")
	if len(snap.Paths) != 2 || snap.Paths[0].Path != "/d" || snap.Paths[1].Path != "/e" {
		t.Fatalf("snapshot = %+v, want /d and /e, the records under /d folded into it", snap.Paths)
	}
	if !snap.Absent(loc("/d/a")) || !snap.Absent(loc("/d/zz")) || snap.Absent(loc("/f")) {
		t.Errorf("snapshot answers wrongly: %+v", snap.Paths)
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

// TestLedger_AnythingButARecordedRemovalDropsEveryDeviceForGood: each result
// that is not a successful, recorded check by a built-in method declaring
// PredictsPaths may stand for something made, whatever it reported, on any
// device that shares a filesystem with another, so every device is read as
// it is for the rest of the run.
func TestLedger_AnythingButARecordedRemovalDropsEveryDeviceForGood(t *testing.T) {
	for name, nr := range map[string]NodeResult{
		"failed":                       {NodeID: "n", Device: "dev-1", Checked: true, Err: errors.New("refused"), predictedSet: true, predicted: []any{}},
		"unchecked":                    {NodeID: "n", Device: "dev-1", Unchecked: true},
		"skipped":                      {NodeID: "n", Device: "dev-1", Checked: true, Skipped: true},
		"a real task":                  {NodeID: "n", Device: "dev-1"},
		"unparsed":                     {NodeID: "n", Device: "dev-1", Checked: true, predictedSet: true, predicted: "unknown"},
		"declaring, recording nothing": {NodeID: "n", Device: "dev-1", Checked: true},
		"undeclared, no change":        {NodeID: "plain", Device: "dev-1", Checked: true, predictedSet: true, predicted: []any{}},
		"undeclared change":            {NodeID: "plain", Device: "dev-1", Checked: true, Changed: true},
		"external, declaring":          {NodeID: "ext", Device: "dev-1", Checked: true, Changed: true, predictedSet: true, predicted: []any{}},
		"a node the DAG does not hold": {NodeID: "gone", Device: "dev-1", Checked: true},
		"another device's task":        {NodeID: "plain", Device: "dev-9", Checked: true},
		"a deviceless task":            {NodeID: "plain", Checked: true},
		"a node-level skip":            {NodeID: "n", Skipped: true},
		"a pause":                      {NodeID: "pause", Device: "dev-1", Checked: true},
		"a failed engine action":       {NodeID: "fact", Device: "dev-1", Checked: true, Err: errors.New("assert")},
		"a record that is not removal": {NodeID: "n", Device: "dev-1", Checked: true, predictedSet: true, predicted: []any{map[string]any{"path": "/x", "state": "present", "dirs": []any{}}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predictedOn("dev-1", absent("/f")), predictedOn("dev-2", absent("/f"))})
			r.recordPredictions([]NodeResult{nr})
			r.recordPredictions([]NodeResult{predicted(absent("/g"))})
			for _, d := range []string{"dev-1", "dev-2", "dev-3"} {
				if snap := r.predicted.snapshot(d); snap != nil {
					t.Errorf("%s kept predictions: %+v", d, snap)
				}
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
		{NodeID: "n", Device: "dev-1", Checked: true, predictedSet: true, predicted: []any{}},
	})
	if snap := r.predicted.snapshot("dev-1"); snap == nil || !snap.Absent(loc("/f")) {
		t.Error("an engine action that touches no device, or a removal that removes nothing, dropped the ledger")
	}
}

// TestLedger_SiblingsRemovingInEitherOrderAgree: removals in either order
// leave the same paths gone.
func TestLedger_SiblingsRemovingInEitherOrderAgree(t *testing.T) {
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(absent("/d/x")), predicted(absent("/d")), predicted(absent("/g"))})
	if snap := r.predicted.snapshot("dev-1"); snap == nil || !snap.Absent(loc("/d/x/y")) || !snap.Absent(loc("/g")) {
		t.Errorf("siblings: %+v", snap)
	}
}

// TestLedger_ADirectoryUnderTwoPathsDropsTheLedger: one identity met under a
// second path (a bind mount), or one path with a second identity (replaced),
// is nothing the ledger models, so it reads the device as it is.
func TestLedger_ADirectoryUnderTwoPathsDropsTheLedger(t *testing.T) {
	for name, second := range map[string]wire.PredictedPath{
		"an identity under a second path": {Path: "/mnt/x", State: wire.PredictedAbsent, Dirs: []wire.DirID{{Path: "/mnt", ID: dirsOf("/d/f")[0].ID}}},
		"a path with a second identity":   {Path: "/d/y", State: wire.PredictedAbsent, Dirs: []wire.DirID{{Path: "/d", ID: "9:9"}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predicted(absent("/d/f"))})
			r.recordPredictions([]NodeResult{predicted(second)})
			if snap := r.predicted.snapshot("dev-1"); snap != nil {
				t.Errorf("snapshot = %+v, want the ledger dropped", snap.Paths)
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
	if snap := r.predicted.snapshot("dev-1"); snap != nil {
		t.Errorf("a ledger past its bound kept %d paths", len(snap.Paths))
	}
}
