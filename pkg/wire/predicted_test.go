// Tests for a device's predicted paths (predicted.go): which paths a
// prediction may name, and what a snapshot vouches for.
package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// at is a location under /d, a directory whose identity is 1:10, with / 1:2.
func at(p string) Location {
	return Location{Path: p, Dirs: []DirID{{Path: "/d", ID: "1:10"}, {Path: "/", ID: "1:2"}}}
}

func TestValidPredictedPath(t *testing.T) {
	dirs := []DirID{{Path: "/", ID: "1:2"}}
	for _, tc := range []struct {
		p    PredictedPath
		want bool
	}{
		{PredictedPath{Path: "/tmp/a", State: PredictedAbsent, Dirs: dirs}, true},
		{PredictedPath{Path: "/", State: PredictedPresent}, true},
		{PredictedPath{Path: "tmp/a", State: PredictedAbsent}, false},
		{PredictedPath{Path: "/tmp/../a", State: PredictedAbsent}, false},
		{PredictedPath{Path: "/tmp/a/", State: PredictedAbsent}, false},
		{PredictedPath{Path: "/tmp/a", State: "gone"}, false},
		{PredictedPath{Path: "", State: PredictedAbsent}, false},
		{PredictedPath{Path: "/tmp/\x00", State: PredictedAbsent}, false},
		{PredictedPath{Path: "/" + strings.Repeat("a", MaxPredictedPathBytes), State: PredictedAbsent}, false},
		{PredictedPath{Path: "/tmp/a", State: PredictedAbsent, Dirs: []DirID{{Path: "/elsewhere", ID: "1:2"}}}, false},
		{PredictedPath{Path: "/tmp/a", State: PredictedAbsent, Dirs: []DirID{{Path: "/tmp", ID: "1"}}}, false},
		{PredictedPath{Path: "/tmp/a", State: PredictedAbsent, Dirs: []DirID{{Path: "/tmp", ID: "1:x"}}}, false},
		{PredictedPath{Path: "/tmp/a", State: PredictedAbsent, Dirs: []DirID{{Path: "/tmp", ID: ":2"}}}, false},
		{PredictedPath{Path: "/tmp/a", State: PredictedAbsent, Dirs: []DirID{{Path: "/tmp", ID: strings.Repeat("1", 40) + ":2"}}}, false},
	} {
		if got := ValidPredictedPath(tc.p); got != tc.want {
			t.Errorf("ValidPredictedPath(%+v) = %v, want %v", tc.p, got, tc.want)
		}
	}
}

func TestPredictions_AbsentFollowsTheNearestRecord(t *testing.T) {
	p := &Predictions{Paths: []PredictedPath{
		{Path: "/d/gone", State: PredictedAbsent},
		{Path: "/d/gone/kept", State: PredictedPresent},
		{Path: "/d/f", State: PredictedAbsent},
		{Path: "/d/g", State: PredictedPresent},
	}, Dirs: []DirID{{Path: "/d", ID: "1:10"}, {Path: "/", ID: "1:2"}}}
	for target, want := range map[string]bool{
		"/d/f":           true,  // recorded gone
		"/d/gone/x":      true,  // under a directory recorded gone
		"/d/gone/kept":   false, // made again after
		"/d/gone/kept/y": false, // under the path made again
		"/d/g":           false, // recorded there
		"/d/elsewhere":   false, // no record
		"/d/../f":        false, // not a physical path
	} {
		if got := p.Absent(at(target)); got != want {
			t.Errorf("Absent(%q) = %v, want %v", target, got, want)
		}
	}
	if !p.Present(at("/d/gone/kept/y")) || p.Present(at("/d/f")) || p.Present(at("/d/none")) {
		t.Error("Present did not follow the nearest record")
	}
	if !p.Recorded(at("/d/f")) || !p.Recorded(at("/d/gone/x")) || p.Recorded(at("/d/none")) {
		t.Error("Recorded did not follow the records at or above")
	}
	var none *Predictions
	if none.Absent(at("/d/f")) || none.Present(at("/d/g")) || none.Recorded(at("/d/f")) {
		t.Error("a nil snapshot vouched for a path")
	}
}

// TestPredictions_ADirectoryUnderTwoPathsVouchesForNothing: one identity
// under a second path is a bind mount or a folded name, and a directory known
// by another identity was replaced; either way the record cannot be trusted.
func TestPredictions_ADirectoryUnderTwoPathsVouchesForNothing(t *testing.T) {
	p := &Predictions{Paths: []PredictedPath{{Path: "/d/f", State: PredictedAbsent}, {Path: "/d/new", State: PredictedPresent}},
		Dirs: []DirID{{Path: "/d", ID: "1:10"}, {Path: "/", ID: "1:2"}}}
	alias := Location{Path: "/d/f", Dirs: []DirID{{Path: "/d", ID: "1:10"}, {Path: "/mnt", ID: "1:10"}}}
	replaced := Location{Path: "/d/f", Dirs: []DirID{{Path: "/d", ID: "1:99"}}}
	for name, l := range map[string]Location{"alias": alias, "replaced": replaced} {
		if p.Absent(l) || p.PresentUnder(Location{Path: "/d", Dirs: l.Dirs}) || p.AbsentUnder(Location{Path: "/d", Dirs: l.Dirs}) {
			t.Errorf("%s: a disagreeing location was vouched for", name)
		}
		if !p.Recorded(l) {
			t.Errorf("%s: a disagreeing location did not read as recorded", name)
		}
	}
}

func TestPredictions_Under(t *testing.T) {
	p := &Predictions{Paths: []PredictedPath{
		{Path: "/d/new", State: PredictedPresent}, {Path: "/e/old", State: PredictedAbsent}, {Path: "/f", State: PredictedAbsent},
	}}
	loc := func(p string) Location { return Location{Path: p} }
	if !p.PresentUnder(loc("/d")) || p.PresentUnder(loc("/e")) || p.PresentUnder(loc("/dd")) || p.PresentUnder(loc("/d/new")) {
		t.Error("PresentUnder answered wrongly")
	}
	if !p.AbsentUnder(loc("/e")) || !p.AbsentUnder(loc("/")) || p.AbsentUnder(loc("/d")) || p.AbsentUnder(loc("/f")) {
		t.Error("AbsentUnder answered wrongly")
	}
	var none *Predictions
	if none.PresentUnder(loc("/d")) || none.AbsentUnder(loc("/e")) {
		t.Error("a nil snapshot answered")
	}
}

// TestChildRequest_CarriesPredictions: the snapshot crosses the process
// boundary whole, and an older parent's request without it decodes to none.
func TestChildRequest_CarriesPredictions(t *testing.T) {
	sent := ChildRequest{FQCN: "file.remove", Params: map[string]any{"path": "/d"},
		Predicted: &Predictions{Paths: []PredictedPath{{Path: "/d/x", State: PredictedAbsent}}, Dirs: at("/d/x").Dirs}}
	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	var got ChildRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Predicted == nil || !got.Predicted.Absent(at("/d/x")) || got.Predicted.Absent(Location{Path: "/d/x", Dirs: []DirID{{Path: "/d", ID: "9:9"}}}) {
		t.Errorf("the snapshot did not cross whole: %+v", got.Predicted)
	}
	var older ChildRequest
	if err := json.Unmarshal([]byte(`{"fqcn":"file.remove","params":{}}`), &older); err != nil || older.Predicted != nil {
		t.Errorf("an older request: %+v, %v", older.Predicted, err)
	}
}
