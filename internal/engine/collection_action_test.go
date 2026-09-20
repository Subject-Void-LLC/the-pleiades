package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These tests cover the bridge that makes the generated Collection catalog
// reachable from a runbook. Before it, pkg/collection was planning-time
// metadata only: 71 registered methods and no execution path that could
// call one.

// recordingFallback is the executor the bridge delegates to for any FQCN
// the Collection registry does not know. It records what it was asked to
// run so a test can prove delegation happened rather than inferring it from
// the absence of an error.
type recordingFallback struct {
	calls  []string
	result engine.ActionResult
	err    error
}

func (f *recordingFallback) Execute(_ context.Context, task *engine.Task, _ inventory.InventoryItem) (engine.ActionResult, error) {
	f.calls = append(f.calls, task.FQCN)
	return f.result, f.err
}

// registerTestMethod registers a Collection method under a name unique to
// the calling test, returning that name.
//
// Registration is process-wide and duplicate names are refused outright, so
// every test needs its own name. It also outlives the test, which is why the
// snapshot below is not optional: without it a second iteration under
// -count>1 finds this name already taken and fails on the duplicate.
func registerTestMethod(t *testing.T, suffix string, status collection.Status, fn collection.Method) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())

	name := "enginetest." + suffix
	if err := collection.Register(collection.Descriptor{
		Name: name,
		// A test fixture still answers the question every real implemented
		// method answers. Not reversible, with the reason registration
		// requires: these fixtures change nothing on any device.
		Manifest: collection.Manifest{Status: status, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"}},
		Invoke:   fn,
	}); err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
	return name
}

// newBridge builds the executor under test over a recording fallback.
func newBridge(fallback *recordingFallback) engine.ActionExecutor {
	return engine.NewCollectionActionExecutor(fallback, engine.NewDeviceRunbookContext)
}

// TestCollectionActionExecutor_InvokesRegisteredMethod proves a registered,
// implemented method actually runs, receives the task's params and the
// target device, and has its emitted facts surfaced as ActionResult.Stats
// so a later task's when_cel can read them.
func TestCollectionActionExecutor_InvokesRegisteredMethod(t *testing.T) {
	var (
		gotParams map[string]any
		gotDevice inventory.InventoryItem
	)

	name := registerTestMethod(t, "invoked", collection.StatusImplemented,
		func(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			gotParams = params
			gotDevice = device
			if err := rc.EmitFact("answer", 42); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{Changed: true}, nil
		})

	fallback := &recordingFallback{}
	device := &inventorytest.Stub{StubName: "sw1"}
	task := &engine.Task{FQCN: name, Params: map[string]any{"key": "value"}}

	result, err := newBridge(fallback).Execute(context.Background(), task, device)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(fallback.calls) != 0 {
		t.Errorf("a registered method must not reach the fallback, got calls %v", fallback.calls)
	}
	if gotParams["key"] != "value" {
		t.Errorf("method received params %v, want the task's own", gotParams)
	}
	if gotDevice == nil || gotDevice.Name() != "sw1" {
		t.Errorf("method received device %v, want the task's target", gotDevice)
	}
	if !result.Changed {
		t.Error("expected Changed to propagate from the method's Result")
	}
	if result.Stats["answer"] != 42 {
		t.Errorf("Stats = %v, want the emitted fact to be surfaced", result.Stats)
	}
}

// TestCollectionActionExecutor_DelegatesUnknownFQCN proves the engine
// keywords keep working. noop and set_metadata are not namespaced and never
// appear in the Collection registry, so the bridge must pass them through
// untouched rather than failing on them.
func TestCollectionActionExecutor_DelegatesUnknownFQCN(t *testing.T) {
	for _, fqcn := range []string{"noop", "set_metadata", "ssh_exec"} {
		t.Run(fqcn, func(t *testing.T) {
			fallback := &recordingFallback{result: engine.ActionResult{Changed: true}}

			result, err := newBridge(fallback).Execute(context.Background(), &engine.Task{FQCN: fqcn}, nil)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if len(fallback.calls) != 1 || fallback.calls[0] != fqcn {
				t.Fatalf("fallback calls = %v, want exactly [%s]", fallback.calls, fqcn)
			}
			if !result.Changed {
				t.Error("expected the fallback's result to propagate unchanged")
			}
		})
	}
}

// TestCollectionActionExecutor_RefusesDeclaredMethod proves the run-time
// half of the declared-is-not-implemented guardrail. internal/validate
// catches this when a runbook is written; this catches a registry that
// changed underneath a plan that was already built.
func TestCollectionActionExecutor_RefusesDeclaredMethod(t *testing.T) {
	name := registerTestMethod(t, "declared", collection.StatusDeclared, nil)

	fallback := &recordingFallback{}
	_, err := newBridge(fallback).Execute(context.Background(), &engine.Task{FQCN: name}, nil)
	if err == nil {
		t.Fatal("expected a declared method to be refused")
	}
	if !strings.Contains(err.Error(), "declared but not implemented") {
		t.Errorf("error = %q, want it to explain the method is a stub", err)
	}
	if len(fallback.calls) != 0 {
		t.Errorf("a declared method must not fall through to the fallback, got %v", fallback.calls)
	}
}

// TestCollectionActionExecutor_PropagatesMethodError proves a failing
// method fails the task, with the method's name attached so an operator can
// tell which one broke.
func TestCollectionActionExecutor_PropagatesMethodError(t *testing.T) {
	sentinel := errors.New("upstream exploded")
	name := registerTestMethod(t, "failing", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, sentinel
		})

	_, err := newBridge(&recordingFallback{}).Execute(context.Background(), &engine.Task{FQCN: name}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap the method's own error", err)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("error = %q, want it to name the failing method", err)
	}
}

// TestRegister_RejectsImplementedWithoutInvoke proves the contradiction is
// caught at registration rather than surfacing as a nil-pointer panic
// partway through a runbook. This is the "type safety moves left" rule the
// rest of the registry follows.
func TestRegister_RejectsImplementedWithoutInvoke(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	err := collection.Register(collection.Descriptor{
		Name:     "enginetest.liar",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"}},
	})
	if err == nil {
		t.Fatal("expected a method claiming implemented with no implementation to be rejected")
	}
	if !strings.Contains(err.Error(), "carries no implementation") {
		t.Errorf("error = %q, want it to explain the contradiction", err)
	}
}

// TestCollectionActionExecutor_WithCollectionInvoker_ReplacesDirectInvoke
// proves a non-nil CollectionInvoker runs instead of desc.Invoke directly,
// the Decorator seam Phase 16 (Native Go Execution Adapter) needs for its
// per-task subprocess boundary: the registered method's own Invoke
// function must never run in-process when an invoker is installed.
func TestCollectionActionExecutor_WithCollectionInvoker_ReplacesDirectInvoke(t *testing.T) {
	var directInvokeCalled bool
	name := registerTestMethod(t, "decorated", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			directInvokeCalled = true
			return collection.Result{}, nil
		})

	var gotFQCN string
	var gotParams map[string]any
	invoker := func(_ context.Context, desc collection.Descriptor, _ inventory.InventoryItem, params map[string]any, _ collection.Mode) (collection.Result, map[string]interface{}, error) {
		gotFQCN = desc.Name
		gotParams = params
		return collection.Result{Changed: true}, map[string]interface{}{"from": "invoker"}, nil
	}

	executor := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext, engine.WithCollectionInvoker(invoker))
	task := &engine.Task{FQCN: name, Params: map[string]any{"key": "value"}}

	result, err := executor.Execute(context.Background(), task, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if directInvokeCalled {
		t.Error("desc.Invoke ran directly; want the installed CollectionInvoker to have run instead")
	}
	if gotFQCN != name {
		t.Errorf("invoker received desc.Name = %q, want %q", gotFQCN, name)
	}
	if gotParams["key"] != "value" {
		t.Errorf("invoker received params %v, want the task's own", gotParams)
	}
	if !result.Changed {
		t.Error("expected Changed to propagate from the invoker's result")
	}
	if result.Stats["from"] != "invoker" {
		t.Errorf("Stats = %v, want the invoker's own stats", result.Stats)
	}
}

// TestCollectionActionExecutor_WithCollectionInvoker_PropagatesError proves
// an invoker's error is wrapped with the failing method's name, identically
// to a direct desc.Invoke failure.
func TestCollectionActionExecutor_WithCollectionInvoker_PropagatesError(t *testing.T) {
	sentinel := errors.New("child process exploded")
	name := registerTestMethod(t, "decorated-failing", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		})

	invoker := func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]any, collection.Mode) (collection.Result, map[string]interface{}, error) {
		return collection.Result{}, nil, sentinel
	}

	executor := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext, engine.WithCollectionInvoker(invoker))
	_, err := executor.Execute(context.Background(), &engine.Task{FQCN: name}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want it to wrap the invoker's own error", err)
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("error = %q, want it to name the failing method", err)
	}
}

// TestCollectionActionExecutor_WithCollectionInvoker_StillRefusesDeclaredMethod
// proves the status check runs before the installed invoker is ever
// consulted: a declared-but-unimplemented method must be refused the same
// way regardless of whether a CollectionInvoker is installed.
func TestCollectionActionExecutor_WithCollectionInvoker_StillRefusesDeclaredMethod(t *testing.T) {
	name := registerTestMethod(t, "decorated-declared", collection.StatusDeclared, nil)

	var invokerCalled bool
	invoker := func(context.Context, collection.Descriptor, inventory.InventoryItem, map[string]any, collection.Mode) (collection.Result, map[string]interface{}, error) {
		invokerCalled = true
		return collection.Result{}, nil, nil
	}

	executor := engine.NewCollectionActionExecutor(&recordingFallback{}, engine.NewDeviceRunbookContext, engine.WithCollectionInvoker(invoker))
	_, err := executor.Execute(context.Background(), &engine.Task{FQCN: name}, nil)
	if err == nil {
		t.Fatal("expected a declared method to be refused")
	}
	if invokerCalled {
		t.Error("the installed CollectionInvoker ran for a declared-but-unimplemented method; want it refused first")
	}
}
