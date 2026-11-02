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
	return NodeResult{NodeID: "n", Device: device, Checked: true, Changed: true, predicted: record, predictedSet: true}
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

// TestLedger_AnythingButARecordingCheckDropsTheDeviceForGood: each result
// that is not a successful, recorded check by a built-in method recording its
// paths may stand for a change nothing recorded, whatever it reported, so the
// device is read as it is for the rest of the run: a later path is located
// against a device that change is missing from.
func TestLedger_AnythingButARecordingCheckDropsTheDeviceForGood(t *testing.T) {
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
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predicted(absent("/f"))})
			r.recordPredictions([]NodeResult{nr})
			if snap := r.predicted.snapshot("dev-1"); snap != nil {
				t.Fatalf("snapshot = %+v, want the device dropped", snap)
			}
			r.recordPredictions([]NodeResult{predicted(absent("/g"))})
			if snap := r.predicted.snapshot("dev-1"); snap != nil {
				t.Errorf("a later record started the device again: %+v", snap)
			}
			if r.predicted.snapshot("dev-2") == nil {
				t.Error("dropping dev-1 dropped dev-2")
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
		t.Error("an engine action that touches no device, or a recording method that changes nothing, dropped the device")
	}
}

// TestLedger_ADevicelessTaskDropsEveryDevice: a task with no device, or a skip
// decided for a whole node, may stand for a change on any device, including
// one no task has reached yet.
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
			r.recordPredictions([]NodeResult{predictedOn("dev-4", absent("/f"))})
			for _, d := range []string{"dev-1", "dev-2", "dev-3", "dev-4", "dev-5"} {
				if r.predicted.snapshot(d) != nil {
					t.Errorf("%s kept predictions", d)
				}
			}
		})
	}
}

// TestLedger_SiblingsThatMayOrderEitherWayDropTheDevice: two nodes of one
// level ran in no order the ledger knows, so records that overlap (one path at
// or under the other's) with different states could leave either answer.
func TestLedger_SiblingsThatMayOrderEitherWayDropTheDevice(t *testing.T) {
	for name, level := range map[string][]NodeResult{
		"one path":              {predicted(absent("/f")), predicted(present("/f"))},
		"a path under another":  {predicted(present("/d/x/y")), predicted(absent("/d/x"))},
		"a sibling dropping it": {predicted(absent("/f")), {NodeID: "plain", Device: "dev-1", Checked: true}},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions(level)
			if r.predicted.snapshot("dev-1") != nil {
				t.Error("siblings that may order either way were recorded")
			}
		})
	}
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(absent("/f")), predicted(absent("/f"), absent("/g")), predicted(present("/h/x"))})
	if snap := r.predicted.snapshot("dev-1"); snap == nil || !snap.Absent(loc("/g")) || !snap.Present(loc("/h/x")) {
		t.Errorf("siblings that agree: %+v", snap)
	}
}

// TestLedger_ADirectoryUnderTwoPathsDropsTheDevice: one identity met under a
// second path is a bind mount, and one path met with a second identity was
// replaced, so nothing on the device can be vouched for.
func TestLedger_ADirectoryUnderTwoPathsDropsTheDevice(t *testing.T) {
	for name, second := range map[string]wire.PredictedPath{
		"an identity under a second path": {Path: "/mnt/x", State: wire.PredictedPresent, Dirs: []wire.DirID{{Path: "/mnt", ID: dirsOf("/d/f")[0].ID}}},
		"a path with a second identity":   {Path: "/d/y", State: wire.PredictedPresent, Dirs: []wire.DirID{{Path: "/d", ID: "9:9"}}},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predicted(absent("/d/f"))})
			r.recordPredictions([]NodeResult{predicted(second)})
			if snap := r.predicted.snapshot("dev-1"); snap != nil {
				t.Errorf("snapshot = %+v, want the device dropped", snap.Paths)
			}
		})
	}
}

// TestLedger_NamesAFilesystemCouldFoldDropTheDevice: on a filesystem that
// folds case (macOS's by default) two names differing only in case are one
// file, and a name that is not ASCII may fold or normalize in ways the ledger
// does not model.
func TestLedger_NamesAFilesystemCouldFoldDropTheDevice(t *testing.T) {
	for name, second := range map[string]wire.PredictedPath{
		"case":      present("/a/FOO"),
		"not ASCII": absent("/a/caf\xc3\xa9"),
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun(t)
			r.recordPredictions([]NodeResult{predicted(absent("/a/foo"))})
			r.recordPredictions([]NodeResult{predicted(second)})
			if r.predicted.snapshot("dev-1") != nil {
				t.Error("a name that could fold was recorded")
			}
		})
	}
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(absent("/a/foo"))})
	r.recordPredictions([]NodeResult{predicted(present("/b/FOO"), present("/a/foo"))})
	if r.predicted.snapshot("dev-1") == nil {
		t.Error("the same name again, or a folding name elsewhere, dropped the device")
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

// TestLedger_APathMadeWhereNothingWasIsFresh: a path made where the real run
// has nothing (removed earlier, or inside a directory made fresh) holds only
// what is recorded under it, and the directories above a path made are there.
func TestLedger_APathMadeWhereNothingWasIsFresh(t *testing.T) {
	r := ledgerRun(t)
	r.recordPredictions([]NodeResult{predicted(absent("/a"), present("/k"))})
	r.recordPredictions([]NodeResult{predicted(present("/a/b/c"), present("/k"), present("/e"))})
	r.recordPredictions([]NodeResult{predicted(present("/a/b/c/d"))})
	snap := r.predicted.snapshot("dev-1")
	for p, want := range map[string]string{"/a": "fresh", "/a/b/c": "fresh", "/a/b/c/d": "fresh", "/k": "present", "/e": "present"} {
		got := "none"
		for _, pp := range snap.Paths {
			if pp.Path == p {
				got = pp.State
			}
		}
		if got != want {
			t.Errorf("%s is %s, want %s", p, got, want)
		}
	}
	if snap.Absent(loc("/a/b")) || !snap.PresentUnder(loc("/a/b")) {
		t.Errorf("a parent of a path made is vouched gone, or empty: %+v", snap.Paths)
	}
}
