// Tests for recording and reading predicted paths (predicted.go).
package sdk_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// statContext is a RunbookContext that keeps the stats a method sets and
// holds a snapshot of predictions.
type statContext struct {
	stats     map[string]any
	predicted *wire.Predictions
}

func (c *statContext) InjectSecrets() map[string]string { return nil }
func (c *statContext) SetStat(key string, v any) error  { c.stats[key] = v; return nil }
func (c *statContext) EmitFact(key string, v any) error { c.stats[key] = v; return nil }
func (c *statContext) Predictions() *wire.Predictions   { return c.predicted }

var rootDirs = []wire.DirID{{Path: "/d", ID: "1:10"}, {Path: "/", ID: "1:2"}}

func TestRecordPredicted_RoundTripsInProcessAndAcrossJSON(t *testing.T) {
	rc := &statContext{stats: map[string]any{}}
	want := []wire.PredictedPath{
		{Path: "/d/x", State: wire.PredictedAbsent, Dirs: rootDirs},
		{Path: "/d/y", State: wire.PredictedPresent, Dirs: []wire.DirID{}},
	}
	if err := sdk.RecordPredicted(rc, want...); err != nil {
		t.Fatal(err)
	}
	got, err := sdk.ParsePredicted(rc.stats[sdk.StatPredictedPaths])
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("in process: %+v, %v", got, err)
	}
	// Across a child boundary the stat is JSON.
	raw, _ := json.Marshal(rc.stats[sdk.StatPredictedPaths])
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got, err := sdk.ParsePredicted(decoded); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("across JSON: %+v, %v", got, err)
	}
}

func TestRecordPredicted_RefusesAPathItCouldNotKey(t *testing.T) {
	rc := &statContext{stats: map[string]any{}}
	for _, p := range []wire.PredictedPath{
		{Path: "rel", State: wire.PredictedAbsent},
		{Path: "/a/../b", State: wire.PredictedAbsent},
		{Path: "/a", State: "maybe"},
		{Path: "/a", State: wire.PredictedAbsent, Dirs: []wire.DirID{{Path: "/b", ID: "1:2"}}},
	} {
		if err := sdk.RecordPredicted(rc, p); err == nil {
			t.Errorf("RecordPredicted(%+v) recorded it", p)
		}
	}
}

func TestParsePredicted_RefusesAnythingElse(t *testing.T) {
	entry := func(extra map[string]any) []any {
		m := map[string]any{"path": "/a", "state": "absent", "dirs": []any{}}
		for k, v := range extra {
			if v == nil {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
		return []any{m}
	}
	for name, v := range map[string]any{
		"unknown":          "unknown",
		"not a map":        []any{"x"},
		"an extra key":     entry(map[string]any{"extra": 1}),
		"no dirs":          entry(map[string]any{"dirs": nil}),
		"dirs not a list":  entry(map[string]any{"dirs": "x"}),
		"a dir not a map":  entry(map[string]any{"dirs": []any{"x"}}),
		"a dir without id": entry(map[string]any{"dirs": []any{map[string]any{"path": "/"}}}),
		"a bad dir id":     entry(map[string]any{"dirs": []any{map[string]any{"path": "/", "id": "x"}}}),
		"a bad path":       entry(map[string]any{"path": "a"}),
		"a wrong state":    entry(map[string]any{"state": 1}),
	} {
		if _, err := sdk.ParsePredicted(v); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	rc := &statContext{stats: map[string]any{}}
	if err := sdk.RecordPredictionUnknown(rc); err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.ParsePredicted(rc.stats[sdk.StatPredictedPaths]); err == nil {
		t.Error("an unknown record parsed as paths")
	}
}

func TestPredicted_ReadsOnlyAContextThatKeepsPredictions(t *testing.T) {
	snap := &wire.Predictions{Paths: []wire.PredictedPath{
		{Path: "/d/x", State: wire.PredictedAbsent}, {Path: "/e/n", State: wire.PredictedPresent},
	}, Dirs: rootDirs}
	loc := func(p string) wire.Location {
		l := wire.Location{Path: p, Dirs: rootDirs[1:]}
		if p == "/d" || len(p) > 3 && p[:3] == "/d/" {
			l.Dirs = rootDirs
		}
		return l
	}
	rc := &statContext{stats: map[string]any{}, predicted: snap}
	if !sdk.PredictionsWanted(rc) || !sdk.PredictedAbsent(rc, loc("/d/x")) || !sdk.PredictedPresent(rc, loc("/e/n")) ||
		!sdk.PredictedRecorded(rc, loc("/d/x")) || !sdk.PredictedPresentUnder(rc, loc("/e")) || !sdk.PredictedAbsentUnder(rc, loc("/d")) {
		t.Error("a context with predictions did not answer")
	}
	if sdk.PredictedAbsent(rc, loc("/d/z")) || sdk.PredictedPresentUnder(rc, loc("/d")) || sdk.PredictedAbsentUnder(rc, loc("/e")) {
		t.Error("answered for a path nothing predicted")
	}
	type bare struct{ sdk.RunbookContext }
	b := bare{rc}
	if sdk.PredictionsWanted(b) || sdk.PredictedAbsent(b, loc("/d/x")) || sdk.PredictedPresent(b, loc("/e/n")) ||
		sdk.PredictedRecorded(b, loc("/d/x")) || sdk.PredictedPresentUnder(b, loc("/e")) || sdk.PredictedAbsentUnder(b, loc("/d")) {
		t.Error("a context without PredictedState answered")
	}
	if sdk.PredictionsWanted(&statContext{stats: map[string]any{}}) {
		t.Error("a context holding no snapshot wants predictions")
	}
	ctx := sdk.WithPredictions(context.Background(), snap)
	if sdk.PredictionsFrom(ctx) != snap || sdk.PredictionsFrom(context.Background()) != nil {
		t.Error("the context carrier lost or invented a snapshot")
	}
}
