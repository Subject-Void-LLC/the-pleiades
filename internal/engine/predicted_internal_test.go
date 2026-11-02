// Tests of the check's prediction ledger (predicted.go): what a level's
// results add to it, and when a device's predictions become unknown.
package engine

import (
	"errors"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ledgerRun is a run holding only what recordPredictions reads: a ledger and
// a DAG whose node "other" names a method the registry does not hold.
func ledgerRun() *run {
	return &run{predicted: newPredictionLedger(), dag: &DAG{Nodes: map[string]*Task{"other": {FQCN: "enginetest.unregistered"}}}}
}

// predicted is a checked result on dev-1 recording paths.
func predicted(paths ...wire.PredictedPath) NodeResult {
	record := make([]any, 0, len(paths))
	for _, p := range paths {
		record = append(record, map[string]any{"path": p.Path, "state": p.State})
	}
	return NodeResult{NodeID: "n", Device: "dev-1", Checked: true, Stats: map[string]any{sdk.StatPredictedPaths: record}}
}

func absent(p string) wire.PredictedPath {
	return wire.PredictedPath{Path: p, State: wire.PredictedAbsent}
}
func present(p string) wire.PredictedPath {
	return wire.PredictedPath{Path: p, State: wire.PredictedPresent}
}

func TestLedger_RecordsAcrossLevelsAndAnAbsentDirectoryTakesItsContents(t *testing.T) {
	r := ledgerRun()
	if r.predicted.snapshot("dev-1") != nil {
		t.Fatal("a device nothing recorded on has a snapshot")
	}
	r.recordPredictions([]NodeResult{predicted(present("/d/a"), present("/d/b"), present("/e"))})
	r.recordPredictions([]NodeResult{predicted(absent("/d"))})
	snap := r.predicted.snapshot("dev-1")
	if snap.Unknown || len(snap.Paths) != 2 || snap.Paths[0] != absent("/d") || snap.Paths[1] != present("/e") {
		t.Fatalf("snapshot = %+v, want /d absent and /e present", snap)
	}
	if !snap.Absent("/d/a") || snap.PresentUnder("/d") {
		t.Errorf("a record under the removed /d survived: %+v", snap)
	}
	if r.predicted.snapshot("dev-2") != nil {
		t.Error("a prediction on dev-1 reached dev-2")
	}
}

func TestLedger_BecomesUnknownAndStaysUnknown(t *testing.T) {
	for name, nr := range map[string]NodeResult{
		"failed":            {NodeID: "n", Device: "dev-1", Checked: true, Err: errors.New("refused")},
		"unchecked":         {NodeID: "n", Device: "dev-1", Unchecked: true},
		"unparsed":          {NodeID: "n", Device: "dev-1", Checked: true, Stats: map[string]any{sdk.StatPredictedPaths: "unknown"}},
		"undeclared change": {NodeID: "other", Device: "dev-1", Checked: true, Changed: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := ledgerRun()
			r.recordPredictions([]NodeResult{predicted(absent("/f"))})
			r.recordPredictions([]NodeResult{nr})
			r.recordPredictions([]NodeResult{predicted(absent("/g"))})
			if snap := r.predicted.snapshot("dev-1"); !snap.Unknown || len(snap.Paths) != 0 {
				t.Errorf("snapshot = %+v, want unknown for the rest of the check", snap)
			}
		})
	}
}

func TestLedger_IgnoresWhatPredictsNothing(t *testing.T) {
	r := ledgerRun()
	r.recordPredictions([]NodeResult{predicted(absent("/f"))})
	r.recordPredictions([]NodeResult{
		{NodeID: "n", Device: "dev-1", Checked: true, Skipped: true, Err: nil},
		{NodeID: "other", Device: "dev-1", Checked: true},                        // read only, no change
		{NodeID: "other", Device: "dev-1", Changed: true},                        // a real task in a mixed run
		{NodeID: "other", Checked: true, Changed: true, Stats: map[string]any{}}, // no device
	})
	if snap := r.predicted.snapshot("dev-1"); snap.Unknown || !snap.Absent("/f") {
		t.Errorf("snapshot = %+v, want /f still predicted absent", snap)
	}
}

// TestLedger_SiblingsThatDisagreeLeaveTheDeviceUnknown: two nodes of one level
// ran in no order the ledger knows, so a path they predict differently is
// neither.
func TestLedger_SiblingsThatDisagreeLeaveTheDeviceUnknown(t *testing.T) {
	r := ledgerRun()
	r.recordPredictions([]NodeResult{predicted(absent("/f")), predicted(present("/f"))})
	if snap := r.predicted.snapshot("dev-1"); !snap.Unknown {
		t.Errorf("snapshot = %+v, want unknown", snap)
	}
	agree := ledgerRun()
	agree.recordPredictions([]NodeResult{predicted(absent("/f")), predicted(absent("/f"), absent("/g"))})
	if snap := agree.predicted.snapshot("dev-1"); snap.Unknown || !snap.Absent("/g") {
		t.Errorf("siblings that agree: %+v", snap)
	}
}

func TestLedger_PastItsBoundBecomesUnknown(t *testing.T) {
	r := ledgerRun()
	paths := make([]wire.PredictedPath, 0, wire.MaxPredictedPaths+1)
	for i := range wire.MaxPredictedPaths + 1 {
		paths = append(paths, present("/p/"+strconv.Itoa(i)))
	}
	r.recordPredictions([]NodeResult{predicted(paths...)})
	if snap := r.predicted.snapshot("dev-1"); !snap.Unknown || len(snap.Paths) != 0 {
		t.Errorf("a ledger past its bound: unknown=%v with %d paths", snap.Unknown, len(snap.Paths))
	}
}

// TestLedger_APathMadeAgainVouchesForNothingRemovedBeneathIt: a link put
// where a removed directory was may hold what was removed, so the old absent
// records beneath it are read from the device again; a path made beneath it
// earlier stays made.
func TestLedger_APathMadeAgainVouchesForNothingRemovedBeneathIt(t *testing.T) {
	r := ledgerRun()
	r.recordPredictions([]NodeResult{predicted(present("/a/kept"))})
	r.recordPredictions([]NodeResult{predicted(absent("/a/old"))})
	r.recordPredictions([]NodeResult{predicted(present("/a"))})
	snap := r.predicted.snapshot("dev-1")
	if snap.Absent("/a/old") {
		t.Error("/a/old, removed before /a was made again, is still vouched gone")
	}
	if !snap.PresentUnder("/a") {
		t.Error("/a/kept, made before, was dropped")
	}
}

// TestLedger_APathMadeBringsItsParentsBack: a directory made with its missing
// parents (file.directory, as mkdir -p) means each parent is there again, so
// one recorded gone is not vouched gone; what else was removed under it is.
func TestLedger_APathMadeBringsItsParentsBack(t *testing.T) {
	r := ledgerRun()
	r.recordPredictions([]NodeResult{predicted(absent("/a"))})
	r.recordPredictions([]NodeResult{predicted(present("/a/b/c"))})
	snap := r.predicted.snapshot("dev-1")
	if snap.Absent("/a") || snap.Absent("/a/b") {
		t.Errorf("a parent of a path made is vouched gone: %+v", snap)
	}
	r.recordPredictions([]NodeResult{predicted(absent("/x/old"))})
	r.recordPredictions([]NodeResult{predicted(present("/x/new"))})
	if snap := r.predicted.snapshot("dev-1"); !snap.Absent("/x/old") {
		t.Errorf("making /x/new brought back /x/old: %+v", snap)
	}
}
