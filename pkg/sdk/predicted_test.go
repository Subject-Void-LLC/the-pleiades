// Tests for recording and reading predicted paths (predicted.go).
package sdk_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// statContext is a RunbookContext that keeps the stats a method sets.
type statContext struct {
	stats     map[string]any
	predicted *wire.Predictions
}

func (c *statContext) InjectSecrets() map[string]string { return nil }
func (c *statContext) SetStat(key string, v any) error  { c.stats[key] = v; return nil }
func (c *statContext) EmitFact(key string, v any) error { c.stats[key] = v; return nil }
func (c *statContext) Predictions() *wire.Predictions   { return c.predicted }

func TestRecordPredicted_RoundTripsInProcessAndAcrossJSON(t *testing.T) {
	rc := &statContext{stats: map[string]any{}}
	want := []wire.PredictedPath{{Path: "/d/x", State: wire.PredictedAbsent}, {Path: "/d/y", State: wire.PredictedPresent}}
	if err := sdk.RecordPredicted(rc, want...); err != nil {
		t.Fatal(err)
	}
	got, err := sdk.ParsePredicted(rc.stats[sdk.StatPredictedPaths])
	if err != nil || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("in process: %+v, %v", got, err)
	}
	// Across a child boundary the stat is JSON.
	raw, _ := json.Marshal(rc.stats[sdk.StatPredictedPaths])
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got, err := sdk.ParsePredicted(decoded); err != nil || len(got) != 2 || got[1] != want[1] {
		t.Errorf("across JSON: %+v, %v", got, err)
	}
}

func TestRecordPredicted_RefusesAPathItCouldNotKey(t *testing.T) {
	rc := &statContext{stats: map[string]any{}}
	for _, p := range []wire.PredictedPath{{Path: "rel", State: wire.PredictedAbsent}, {Path: "/a/../b", State: wire.PredictedAbsent}, {Path: "/a", State: "maybe"}} {
		if err := sdk.RecordPredicted(rc, p); err == nil {
			t.Errorf("RecordPredicted(%+v) recorded it", p)
		}
	}
}

func TestParsePredicted_RefusesAnythingElse(t *testing.T) {
	for name, v := range map[string]any{
		"unknown":       "unknown",
		"not a map":     []any{"x"},
		"an extra key":  []any{map[string]any{"path": "/a", "state": "absent", "extra": 1}},
		"a bad path":    []any{map[string]any{"path": "a", "state": "absent"}},
		"a wrong state": []any{map[string]any{"path": "/a", "state": 1}},
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

func TestPredictedAbsent_ReadsOnlyAContextThatKeepsPredictions(t *testing.T) {
	snap := &wire.Predictions{Paths: []wire.PredictedPath{{Path: "/d/x", State: wire.PredictedAbsent}, {Path: "/e/n", State: wire.PredictedPresent}}}
	rc := &statContext{stats: map[string]any{}, predicted: snap}
	if !sdk.PredictedAbsent(rc, "/d/x") || !sdk.PredictedPresentUnder(rc, "/e") {
		t.Error("a context with predictions did not answer")
	}
	if !sdk.PredictedAbsentUnder(rc, "/d") || sdk.PredictedAbsentUnder(rc, "/e") {
		t.Error("PredictedAbsentUnder did not answer from the snapshot")
	}
	if sdk.PredictedAbsent(rc, "/d/z") || sdk.PredictedPresentUnder(rc, "/d") {
		t.Error("answered for a path nothing predicted")
	}
	type bare struct{ sdk.RunbookContext }
	if sdk.PredictedAbsent(bare{rc}, "/d/x") || sdk.PredictedPresentUnder(bare{rc}, "/e") || sdk.PredictedAbsentUnder(bare{rc}, "/d") {
		t.Error("a context without PredictedState answered")
	}
	ctx := sdk.WithPredictions(context.Background(), snap)
	if sdk.PredictionsFrom(ctx) != snap || sdk.PredictionsFrom(context.Background()) != nil {
		t.Error("the context carrier lost or invented a snapshot")
	}
}
