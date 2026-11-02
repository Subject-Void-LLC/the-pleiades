// Tests that a check's predictions reach a later task's method (Phase 46a):
// the ledger is written at each level's barrier and its snapshot is handed to
// the method through sdk.PredictedState.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// registerPredictingMethods registers enginepredict.remove, whose check
// predicts its path gone (and, when declares is false, without declaring
// PredictsPaths), and enginepredict.observe, whose check and real run record
// what the snapshot says of its path.
func registerPredictingMethods(t *testing.T, declares bool) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	pathOf := func(params map[string]any) string { p, _ := params["path"].(string); return p }
	remove := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, sdk.RecordPredicted(rc, wire.PredictedPath{Path: pathOf(params), State: wire.PredictedAbsent})
	}
	observe := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		state, ok := rc.(sdk.PredictedState)
		if err := rc.SetStat("absent", sdk.PredictedAbsent(rc, pathOf(params))); err != nil {
			return collection.Result{}, err
		}
		if err := rc.SetStat("unknown", ok && state.Predictions() != nil && state.Predictions().Unknown); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{}, rc.SetStat("snapshot", ok && state.Predictions() != nil)
	}
	for _, d := range []collection.Descriptor{
		{Name: "enginepredict.remove", Invoke: remove, Check: remove, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, SupportsCheck: true, PredictsPaths: declares,
			Reversibility: collection.Reversibility{Notes: "a test fixture"}}},
		{Name: "enginepredict.observe", Invoke: observe, Check: observe, Manifest: collection.Manifest{
			Status: collection.StatusImplemented, SupportsCheck: true, PredictsPaths: true,
			Reversibility: collection.Reversibility{Notes: "a test fixture"}}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatalf("registering %s: %v", d.Name, err)
		}
	}
}

// predictionDAG removes /d/x on the fleet, then observes /d/x and /d/y.
func predictionDAG(t *testing.T) *engine.DAG {
	return buildDAG(t, `{"id": "predict", "tasks": [
		{"name": "remove", "fqcn": "enginepredict.remove", "params": {"target": "fleet", "path": "/d/x"}},
		{"name": "gone", "fqcn": "enginepredict.observe", "params": {"target": "fleet", "path": "/d/x"}},
		{"name": "kept", "fqcn": "enginepredict.observe", "params": {"target": "fleet", "path": "/d/y"}}
	]}`)
}

func predictionFleet() mapResolver {
	return mapResolver{"fleet": {
		&inventorytest.Stub{StubID: "dev-1", StubName: "dev-1", StubState: inventory.StateActive},
		&inventorytest.Stub{StubID: "dev-2", StubName: "dev-2", StubState: inventory.StateActive},
	}}
}

// stat returns node nodeID's stat key on device.
func stat(t *testing.T, result engine.RunResult, nodeID, device, key string) any {
	t.Helper()
	for _, n := range result.Nodes {
		if n.NodeID == nodeID && n.Device == device {
			if n.Err != nil {
				t.Fatalf("%s on %s failed: %v", nodeID, device, n.Err)
			}
			return n.Stats[key]
		}
	}
	t.Fatalf("no result for %s on %s: %+v", nodeID, device, result.Nodes)
	return nil
}

func TestCheckPredictions_ReachALaterTaskOnEachDevice(t *testing.T) {
	registerPredictingMethods(t, true)
	result, err := newCheckExecutor(predictionFleet(), engine.WithMode(collection.ModeCheck)).Run(context.Background(), predictionDAG(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, device := range []string{"dev-1", "dev-2"} {
		if stat(t, result, "tasks[1]", device, "absent") != true {
			t.Errorf("%s: the later task did not see /d/x predicted gone", device)
		}
		if stat(t, result, "tasks[2]", device, "absent") != false {
			t.Errorf("%s: /d/y, which nothing removed, was predicted gone", device)
		}
	}
}

// TestCheckPredictions_AnUndeclaredChangeLeavesTheDeviceUnknown: a method that
// does not declare it records every path it changes may have changed one it
// did not name, so what it did name is not vouched for either.
func TestCheckPredictions_AnUndeclaredChangeLeavesTheDeviceUnknown(t *testing.T) {
	registerPredictingMethods(t, false)
	result, err := newCheckExecutor(predictionFleet(), engine.WithMode(collection.ModeCheck)).Run(context.Background(), predictionDAG(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stat(t, result, "tasks[1]", "dev-1", "absent") != false || stat(t, result, "tasks[1]", "dev-1", "unknown") != true {
		t.Error("an undeclared method's prediction was vouched for")
	}
}

// TestCheckPredictions_ARealRunHasNoneAndMayRecordNone: a real run reads the
// device as it is, and a real run's result recording predicted paths is
// refused, since only a check predicts.
func TestCheckPredictions_ARealRunHasNoneAndMayRecordNone(t *testing.T) {
	registerPredictingMethods(t, true)
	result, err := newCheckExecutor(predictionFleet()).Run(context.Background(), buildDAG(t, `{"id": "real", "tasks": [
		{"name": "gone", "fqcn": "enginepredict.observe", "params": {"target": "fleet", "path": "/d/x"}}
	]}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stat(t, result, "tasks[0]", "dev-1", "snapshot") != false {
		t.Error("a real run's method was handed a snapshot")
	}
	result, _ = newCheckExecutor(predictionFleet()).Run(context.Background(), buildDAG(t, `{"id": "real", "tasks": [
		{"name": "remove", "fqcn": "enginepredict.remove", "params": {"target": "fleet", "path": "/d/x"}}
	]}`))
	n := nodeByDevice(t, result, "tasks[0]", "dev-1")
	if n.Err == nil || !strings.Contains(n.Err.Error(), sdk.StatPredictedPaths) {
		t.Errorf("a real run recording predicted paths: %v, want a refusal naming %q", n.Err, sdk.StatPredictedPaths)
	}
}

// nodeByDevice returns node nodeID's result on device.
func nodeByDevice(t *testing.T, result engine.RunResult, nodeID, device string) engine.NodeResult {
	t.Helper()
	for _, n := range result.Nodes {
		if n.NodeID == nodeID && n.Device == device {
			return n
		}
	}
	t.Fatalf("no result for %s on %s", nodeID, device)
	return engine.NodeResult{}
}
