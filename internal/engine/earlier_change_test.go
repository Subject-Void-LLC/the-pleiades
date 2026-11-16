// Tests that a check tells a later task's built-in method whether an earlier
// checked task on its device predicted a change, could not be checked, or
// failed (sdk.EarlierChanges).
package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerEarlierMethods registers engineearlier.change, whose check predicts
// a change on the device its "on" parameter names (and, given "skip", cannot
// check it), and engineearlier.observe, which records what it was told.
func registerEarlierMethods(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	change := func(_ context.Context, _ sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		if params["skip"] == device.Name() {
			return collection.Result{}, collection.CannotCheck("a test fixture that cannot check here")
		}
		return collection.Result{Changed: params["on"] == device.Name()}, nil
	}
	observe := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		return collection.Result{}, rc.SetStat("earlier", sdk.EarlierChangePredicted(rc))
	}
	for _, d := range []collection.Descriptor{
		{Name: "engineearlier.change", Invoke: change, Check: change, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{Notes: "a test fixture"}}},
		{Name: "engineearlier.observe", Invoke: observe, Check: observe, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, SupportsCheck: true, Reversibility: collection.Reversibility{Notes: "a test fixture"}}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatalf("registering %s: %v", d.Name, err)
		}
	}
}

func TestEarlierChange_ReachesALaterCheckOnTheSameDeviceOnly(t *testing.T) {
	registerEarlierMethods(t)
	result, err := newCheckExecutor(predictionFleet(), engine.WithMode(collection.ModeCheck)).Run(context.Background(), buildDAG(t, `{"id": "earlier", "tasks": [
		{"name": "first", "fqcn": "engineearlier.observe", "params": {"target": "fleet"}},
		{"name": "change", "fqcn": "engineearlier.change", "params": {"target": "fleet", "on": "dev-1"}},
		{"name": "later", "fqcn": "engineearlier.observe", "params": {"target": "fleet"}}
	]}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for device, want := range map[string]bool{"dev-1": true, "dev-2": false} {
		if got := stat(t, result, "tasks[0]", device, "earlier"); got != false {
			t.Errorf("%s: the first task was told of an earlier change: %v", device, got)
		}
		if got := stat(t, result, "tasks[2]", device, "earlier"); got != want {
			t.Errorf("%s: the later task was told %v, want %v", device, got, want)
		}
	}
}

func TestEarlierChange_AnUncheckedTaskCountsAndARealRunsChangeDoesNot(t *testing.T) {
	registerEarlierMethods(t)
	check, err := newCheckExecutor(predictionFleet(), engine.WithMode(collection.ModeCheck)).Run(context.Background(), buildDAG(t, `{"id": "earlier", "tasks": [
		{"name": "change", "fqcn": "engineearlier.change", "params": {"target": "fleet", "on": "nobody", "skip": "dev-2"}},
		{"name": "later", "fqcn": "engineearlier.observe", "params": {"target": "fleet"}}
	]}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := stat(t, check, "tasks[1]", "dev-2", "earlier"); got != true {
		t.Errorf("after a task that could not be checked, the later check was told %v", got)
	}
	if got := stat(t, check, "tasks[1]", "dev-1", "earlier"); got != false {
		t.Errorf("after a check predicting no change, the later check was told %v", got)
	}
	// In a real run a real task's change is on the device, which a later
	// check_mode task reads as it is.
	applied, err := newCheckExecutor(predictionFleet()).Run(context.Background(), buildDAG(t, `{"id": "earlier-real", "tasks": [
		{"name": "change", "fqcn": "engineearlier.change", "params": {"target": "fleet", "on": "dev-1"}},
		{"name": "later", "fqcn": "engineearlier.observe", "check_mode": true, "params": {"target": "fleet"}}
	]}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !nodeByDevice(t, applied, "tasks[1]", "dev-1").Checked {
		t.Fatal("the check_mode task ran for real, so this proves nothing")
	}
	if got := stat(t, applied, "tasks[1]", "dev-1", "earlier"); got != false {
		t.Errorf("after a real task's change, the later check was told %v", got)
	}
}
