// Package engine_test: tests of the predicted marker every check result
// carries.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerDiffMethod registers a method whose run and check both record a
// diff and, when claim is set, a predicted stat of their own saying claim.
func registerDiffMethod(t *testing.T, name string, claim any) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	body := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		if err := sdk.RecordDiff(rc, sdk.Diff{Before: map[string]any{"v": 1}, After: map[string]any{"v": 2}}); err != nil {
			return collection.Result{}, err
		}
		if claim != nil {
			if err := rc.SetStat(sdk.StatPredicted, claim); err != nil {
				return collection.Result{}, err
			}
		}
		return collection.Result{Changed: true}, nil
	}
	if err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture"},
			SupportsCheck: true,
		},
		Invoke: body, Check: body,
	}); err != nil {
		t.Fatal(err)
	}
}

// predicted reports whether stats carry the prediction marker at the top
// and inside the diff.
func predicted(stats map[string]any) (top, inDiff bool) {
	top = stats[sdk.StatPredicted] == true
	if diff, ok := stats[sdk.StatDiff].(map[string]any); ok {
		inDiff = diff[sdk.DiffPredicted] == true
	}
	return top, inDiff
}

// TestPrediction_ACheckSaysItIsOne proves a check's result carries the
// marker, on the result and inside its diff, and that the same method's
// real run carries neither: the control that shows the engine, not the
// method, put it there.
func TestPrediction_ACheckSaysItIsOne(t *testing.T) {
	registerDiffMethod(t, "enginepredict.plain", nil)
	dag := buildDAG(t, `{"id": "predict", "tasks": [{"name": "t", "fqcn": "enginepredict.plain"}]}`)

	checked, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatal(err)
	}
	if top, inDiff := predicted(nodeByID(t, checked, "tasks[0]").Stats); !top || !inDiff {
		t.Errorf("a check's result: predicted on the result %v, in the diff %v; want both", top, inDiff)
	}

	real, err := newCheckExecutor(mapResolver{}).Run(context.Background(), dag)
	if err != nil {
		t.Fatal(err)
	}
	if top, inDiff := predicted(nodeByID(t, real, "tasks[0]").Stats); top || inDiff {
		t.Errorf("a real run's result says it is a prediction (%v, %v)", top, inDiff)
	}
}

// TestPrediction_AMethodCannotSayOtherwise proves the marker belongs to
// the engine: a check that sets it false is overwritten, and a real run
// that sets it at all is refused.
func TestPrediction_AMethodCannotSayOtherwise(t *testing.T) {
	registerDiffMethod(t, "enginepredict.liar", false)
	dag := buildDAG(t, `{"id": "predict-liar", "tasks": [{"name": "t", "fqcn": "enginepredict.liar"}]}`)

	checked, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
	if err != nil {
		t.Fatal(err)
	}
	if top, _ := predicted(nodeByID(t, checked, "tasks[0]").Stats); !top {
		t.Error("a check that set predicted to false kept it")
	}

	real, err := newCheckExecutor(mapResolver{}).Run(context.Background(), dag)
	if err != nil {
		t.Fatal(err)
	}
	node := nodeByID(t, real, "tasks[0]")
	if node.Err == nil || !strings.Contains(node.Err.Error(), "says it is a prediction") {
		t.Errorf("a real run claiming to be a prediction = %v, want it refused", node.Err)
	}
}

// TestPrediction_ARegisteredResultCarriesIt proves a later task reads the
// marker in a checked task's registered result, so a condition built on a
// prediction can see that it is one.
func TestPrediction_ARegisteredResultCarriesIt(t *testing.T) {
	registerDiffMethod(t, "enginepredict.reg", nil)
	for _, tc := range []struct {
		cond    string
		skipped bool
	}{
		{cond: `stat.r[\"\"].predicted == true`, skipped: false},
		{cond: `stat.r[\"\"].predicted == false`, skipped: true},
	} {
		dag := buildDAG(t, `{"id": "predict-reg", "tasks": [
			{"name": "probe", "fqcn": "enginepredict.reg", "register": "r"},
			{"name": "reader", "fqcn": "noop", "when_cel": "`+tc.cond+`"}]}`)
		result, err := newCheckExecutor(mapResolver{}, engine.WithMode(collection.ModeCheck)).Run(context.Background(), dag)
		if err != nil {
			t.Fatal(err)
		}
		if got := nodeByID(t, result, "tasks[1]").Skipped; got != tc.skipped {
			t.Errorf("with %s the reader was skipped = %v, want %v", tc.cond, got, tc.skipped)
		}
	}
}
