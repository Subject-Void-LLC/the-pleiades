// Tests for a device's predicted paths (predicted.go): which paths a
// prediction may name, and what a snapshot vouches for.
package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidPredictedPath(t *testing.T) {
	for _, tc := range []struct {
		p    PredictedPath
		want bool
	}{
		{PredictedPath{"/tmp/a", PredictedAbsent}, true},
		{PredictedPath{"/", PredictedPresent}, true},
		{PredictedPath{"tmp/a", PredictedAbsent}, false},
		{PredictedPath{"/tmp/../a", PredictedAbsent}, false},
		{PredictedPath{"/tmp/a/", PredictedAbsent}, false},
		{PredictedPath{"/tmp/a", "gone"}, false},
		{PredictedPath{"", PredictedAbsent}, false},
		{PredictedPath{"/tmp/\x00", PredictedAbsent}, false},
		{PredictedPath{"/" + strings.Repeat("a", MaxPredictedPathBytes), PredictedAbsent}, false},
	} {
		if got := ValidPredictedPath(tc.p); got != tc.want {
			t.Errorf("ValidPredictedPath(%q, %s) = %v, want %v", tc.p.Path, tc.p.State, got, tc.want)
		}
	}
}

func TestPredictions_AbsentFollowsTheNearestRecord(t *testing.T) {
	p := &Predictions{Paths: []PredictedPath{
		{"/d", PredictedAbsent},
		{"/d/kept", PredictedPresent},
		{"/f", PredictedAbsent},
		{"/g", PredictedPresent},
	}}
	for target, want := range map[string]bool{
		"/f":         true,  // recorded gone
		"/d/x":       true,  // under a directory recorded gone
		"/d/kept":    false, // made again after
		"/d/kept/y":  false, // under the path made again
		"/g":         false, // recorded there
		"/elsewhere": false, // no record
		"/d/../f":    false, // not cleaned
		"relative":   false,
	} {
		if got := p.Absent(target); got != want {
			t.Errorf("Absent(%q) = %v, want %v", target, got, want)
		}
	}
	var none *Predictions
	if none.Absent("/f") || (&Predictions{Unknown: true, Paths: p.Paths}).Absent("/f") {
		t.Error("a nil or unknown snapshot vouched for a path")
	}
}

func TestPredictions_PresentUnder(t *testing.T) {
	p := &Predictions{Paths: []PredictedPath{{"/d/new", PredictedPresent}, {"/e/old", PredictedAbsent}}}
	if !p.PresentUnder("/d") || !p.PresentUnder("/d/") {
		t.Error("a path made inside /d was not seen")
	}
	if p.PresentUnder("/e") || p.PresentUnder("/dd") || p.PresentUnder("/d/new") {
		t.Error("PresentUnder answered for a directory nothing was made in")
	}
	if (&Predictions{Unknown: true, Paths: p.Paths}).PresentUnder("/d") {
		t.Error("an unknown snapshot answered")
	}
}

// TestChildRequest_CarriesPredictions: the snapshot crosses the process
// boundary whole, and an older parent's request without it decodes to none.
func TestChildRequest_CarriesPredictions(t *testing.T) {
	sent := ChildRequest{FQCN: "file.remove", Params: map[string]any{"path": "/d"},
		Predicted: &Predictions{Paths: []PredictedPath{{"/d/x", PredictedAbsent}}}}
	raw, err := json.Marshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	var got ChildRequest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Predicted == nil || !got.Predicted.Absent("/d/x") {
		t.Errorf("the snapshot did not cross: %+v", got.Predicted)
	}
	var older ChildRequest
	if err := json.Unmarshal([]byte(`{"fqcn":"file.remove","params":{}}`), &older); err != nil || older.Predicted != nil {
		t.Errorf("an older request: %+v, %v", older.Predicted, err)
	}
}

func TestPredictions_AbsentUnder(t *testing.T) {
	p := &Predictions{Paths: []PredictedPath{{"/d/gone", PredictedAbsent}, {"/e/new", PredictedPresent}, {"/f", PredictedAbsent}}}
	if !p.AbsentUnder("/d") || !p.AbsentUnder("/") {
		t.Error("a path removed inside was not seen")
	}
	if p.AbsentUnder("/e") || p.AbsentUnder("/f") || p.AbsentUnder("/d/gone") {
		t.Error("AbsentUnder answered for a directory nothing was removed in")
	}
	if (&Predictions{Unknown: true, Paths: p.Paths}).AbsentUnder("/d") || (*Predictions)(nil).AbsentUnder("/d") {
		t.Error("a nil or unknown snapshot answered")
	}
}
