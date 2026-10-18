// Tests for what a failed task keeps: the stats its method recorded before
// failing (a failed command's exit status and output), reported and masked
// as a successful task's are, less the undo it recorded, and nothing at
// all for a task held for review.
package engine_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// failingOutput is what the failing fixture writes to standard output, long
// enough for register_mask to mask.
const failingOutput = "partial output 0123456789"

// recordsThenFails is exec.command's shape: it records its exit status, its
// output and an undo, then fails on the exit status.
func recordsThenFails(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	_ = rc.SetStat("rc", 3)
	_ = rc.SetStat("stdout", failingOutput)
	_ = rc.SetStat(sdk.StatInverse, map[string]any{"fqcn": "enginetest.undo", "params": map[string]any{}})
	return collection.Result{Changed: true}, errors.New("command exited 3")
}

// TestCollectionActionExecutor_AFailureKeepsWhatItRecorded: in process and
// through a CollectionInvoker alike, a failed method's exit status and
// output come back beside its error, and its undo does not.
func TestCollectionActionExecutor_AFailureKeepsWhatItRecorded(t *testing.T) {
	name := registerTestMethod(t, "records-then-fails", collection.StatusImplemented, recordsThenFails)
	check := func(t *testing.T, result engine.ActionResult, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("the failure was lost")
		}
		if result.Stats["rc"] != 3 || result.Stats["stdout"] != failingOutput {
			t.Errorf("stats = %v, want the exit status and output", result.Stats)
		}
		if _, undo := result.Stats[sdk.StatInverse]; undo || result.Changed {
			t.Errorf("a failure kept its undo or a change: %+v", result)
		}
	}

	result, err := newBridge(&recordingFallback{}).Execute(context.Background(), &engine.Task{FQCN: name}, &inventorytest.Stub{StubName: "dev"})
	check(t, result, err)

	invoker := func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]any, collection.Mode) (collection.Result, map[string]any, error) {
		return collection.Result{}, map[string]any{"rc": 3, "stdout": failingOutput, sdk.StatInverse: map[string]any{}}, errors.New("command exited 3")
	}
	bridged := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext, engine.WithCollectionInvoker(invoker))
	result, err = bridged.Execute(context.Background(), &engine.Task{FQCN: name}, &inventorytest.Stub{StubName: "dev"})
	check(t, result, err)
}

// statsAction is an action executor that answers every task with result
// and err, standing in for one that is not the collection bridge.
type statsAction struct {
	result engine.ActionResult
	err    error
}

func (a statsAction) Execute(context.Context, *engine.Task, inventory.InventoryItem) (engine.ActionResult, error) {
	return a.result, a.err
}

// TestExecutor_AFailureNeverReportsAnUndo: whatever produced a failed
// node's stats, its undo is left out: an action executor that fails with
// one among its stats, and a real run refused because it answered with a
// prediction (whose stats arrive success-shaped).
func TestExecutor_AFailureNeverReportsAnUndo(t *testing.T) {
	device := &inventorytest.Stub{StubID: "dev", StubName: "dev", StubState: inventory.StateActive}
	undo := map[string]any{"fqcn": "enginetest.undo", "params": map[string]any{}}
	for name, action := range map[string]statsAction{
		"a failure with an undo": {engine.ActionResult{Stats: map[string]any{"rc": 3, sdk.StatInverse: undo}}, errors.New("command exited 3")},
		"a refused prediction":   {engine.ActionResult{Changed: true, Stats: map[string]any{"rc": 0, sdk.StatPredicted: true, sdk.StatInverse: undo}}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			dag := buildDAG(t, `{"id": "u", "tasks": [{"name": "t", "fqcn": "noop", "params": {"target": "all"}}]}`)
			x := engine.NewExecutor(mapResolver{"all": {device}}, action, lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
			result, err := x.Run(context.Background(), dag)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if len(result.Nodes) != 1 || result.Nodes[0].Err == nil {
				t.Fatalf("nodes %+v, want one failed", result.Nodes)
			}
			stats := result.Nodes[0].Stats
			if _, ok := stats[sdk.StatInverse]; ok || stats["rc"] == nil {
				t.Errorf("the failed node's stats = %v, want its output without its undo", stats)
			}
		})
	}
}

// TestExecutor_AFailedTaskReportsItsOutput: through a whole run, the failed
// node carries the method's output for the caller to print, and the task's
// register_mask still marks what it protects; a node held for review
// carries none of what its method recorded.
func TestExecutor_AFailedTaskReportsItsOutput(t *testing.T) {
	failing := registerTestMethod(t, "run-records-then-fails", collection.StatusImplemented, recordsThenFails)
	held := registerTestMethod(t, "run-holds", collection.StatusImplemented,
		func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			_ = rc.SetStat("answer", "the answer awaiting a person")
			return collection.Result{}, &sdk.HeldError{Key: "k1", Reason: "held for review"}
		})
	device := &inventorytest.Stub{StubID: "dev", StubName: "dev", StubState: inventory.StateActive}
	run := func(fqcn, extra string) engine.RunResult {
		t.Helper()
		dag := buildDAG(t, `{"id": "f", "tasks": [{"name": "t", "fqcn": "`+fqcn+`", "params": {"target": "all"}`+extra+`}]}`)
		x := engine.NewExecutor(mapResolver{"all": {device}}, newBridge(&recordingFallback{}), lock.NewInProcessManager(), event.NewInProcessBus(), engine.NewInProcessWorkflowContext(), 0)
		result, err := x.Run(context.Background(), dag)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(result.Nodes) != 1 || result.Nodes[0].Err == nil {
			t.Fatalf("nodes %+v, want one failed", result.Nodes)
		}
		return result
	}

	result := run(failing, `, "register_mask": "stdout"`)
	stats := result.Nodes[0].Stats
	if stats["rc"] != 3 || stats["stdout"] != failingOutput {
		t.Errorf("the failed node's stats = %v, want its exit status and output", stats)
	}
	if _, undo := stats[sdk.StatInverse]; undo {
		t.Errorf("the failed node reports an undo: %v", stats)
	}
	if !slices.Contains(result.Secrets, failingOutput) {
		t.Errorf("register_mask did not mark the failed task's output: %v", result.Secrets)
	}

	if stats := run(held, "").Nodes[0].Stats; len(stats) != 0 {
		t.Errorf("a held node reports %v; what it holds is for a person first", stats)
	}
}
