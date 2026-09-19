// Package native: the "cannot check this call" answer across the Runner's per-
// task child process.
package native

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// nativeIPCPartlyMethodName is a method whose Check can answer only when
// creates is set, registered at init for the reason
// nativeIPCEchoMethodName is: so the re-exec'd child has it too. Its
// reason quotes the device's password, to prove the parent masks it.
const nativeIPCPartlyMethodName = "nativeipctest.partly"

func init() {
	cannot := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		if params["creates"] == nil {
			return collection.Result{Changed: true}, collection.CannotCheck("no creates, and the password is " + rc.InjectSecrets()["password"])
		}
		return collection.Result{Changed: true}, rc.SetStat("would_run", true)
	}
	collection.MustRegister(collection.Descriptor{
		Name: nativeIPCPartlyMethodName,
		Manifest: collection.Manifest{
			Status:        collection.StatusImplemented,
			Reversibility: collection.Reversibility{Notes: "a test fixture"},
			SupportsCheck: true,
		},
		Invoke: cannot,
		Check:  cannot,
	})
}

// TestIPCCollectionExecutor_CannotCheckCrossesTheProcessBoundary covers
// the Runner's half of the "cannot check this call" answer, through a real
// child process: in a check it arrives as a CannotCheckError with the
// reason masked, a call the method can check still succeeds, and the same
// answer from a real run is an ordinary failure.
func TestIPCCollectionExecutor_CannotCheckCrossesTheProcessBoundary(t *testing.T) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}
	device := newWireDevice(wire.DispatchPayload{
		JobID: "job-1", DeviceID: "dev-1", DeviceName: "router1", DeviceHost: "10.0.0.1", SSHPort: 22,
		Secrets: map[string]string{"username": "admin", "password": "hunter2"},
	})
	desc := collection.Descriptor{Name: nativeIPCPartlyMethodName}

	_, facts, err := exec.invoke(context.Background(), desc, device, nil, collection.ModeCheck)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) {
		t.Fatalf("a check the method cannot answer = %v (facts %v), want a CannotCheckError", err, facts)
	}
	if strings.Contains(cannot.Reason, "hunter2") || !strings.Contains(cannot.Reason, "no creates") {
		t.Errorf("reason = %q, want the method's reason with the password masked", cannot.Reason)
	}

	result, facts, err := exec.invoke(context.Background(), desc, device, map[string]any{"creates": "/tmp/x"}, collection.ModeCheck)
	if err != nil || !result.Changed || facts["would_run"] != true {
		t.Errorf("a check the method can answer = %+v, %v, %v", result, facts, err)
	}

	_, _, err = exec.invoke(context.Background(), desc, device, nil, collection.ModeExecute)
	if err == nil || errors.As(err, &cannot) {
		t.Errorf("a real run answered cannot-check = %v, want an ordinary failure", err)
	}
}

// TestChildAnswer_WhatAChildCanSayBack covers each answer a child's
// response can carry, including the two the shared child code never sends
// but a child binary that disagrees with this one could: "cannot check" in
// a real run is refused rather than read as success or as an ordinary
// failure's text, and "cannot check" with no reason still names that it
// has none. Every text the child sent is masked.
func TestChildAnswer_WhatAChildCanSayBack(t *testing.T) {
	secrets := []string{"hunter2"}
	var cannot *collection.CannotCheckError

	_, _, err := childAnswer("m", collection.ModeExecute, wire.ChildResponse{CannotCheck: true, Error: "no"}, secrets)
	if err == nil || errors.As(err, &cannot) || !strings.Contains(err.Error(), "cannot check a call that was not a check") {
		t.Errorf("cannot-check in a real run = %v, want a refusal that is not a CannotCheckError", err)
	}

	_, _, err = childAnswer("m", collection.ModeCheck, wire.ChildResponse{CannotCheck: true}, secrets)
	if !errors.As(err, &cannot) || cannot.Reason != "the method gave no reason" {
		t.Errorf("cannot-check with no reason = %v, want a CannotCheckError saying none was given", err)
	}

	_, _, err = childAnswer("m", collection.ModeExecute, wire.ChildResponse{Error: "login as hunter2 failed"}, secrets)
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("a failure = %v, want it with the secret masked", err)
	}

	result, facts, err := childAnswer("m", collection.ModeExecute, wire.ChildResponse{Changed: true, Facts: map[string]interface{}{"k": "v"}}, secrets)
	if err != nil || !result.Changed || facts["k"] != "v" {
		t.Errorf("a success = %+v, %v, %v", result, facts, err)
	}
}

// TestInvokeInProcess_PassesFailuresThrough covers the in-process route an
// external Collection's proxy takes: a check of a method with no check
// support is refused before anything runs, and a method's own failure
// comes back unchanged, so the engine sees the same errors either route.
func TestInvokeInProcess_PassesFailuresThrough(t *testing.T) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}
	failure := errors.New("the device said no")
	ran := false
	desc := collection.Descriptor{
		Name: "nativeipctest.inprocess",
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			ran = true
			return collection.Result{}, failure
		},
	}
	if _, _, err := exec.invokeInProcess(context.Background(), desc, nil, nil, collection.ModeCheck, nil); err == nil || !strings.Contains(err.Error(), "does not support check mode") {
		t.Errorf("a check with no check support = %v, want a refusal", err)
	}
	if ran {
		t.Error("the method ran for a check it does not support")
	}
	if _, _, err := exec.invokeInProcess(context.Background(), desc, nil, nil, collection.ModeExecute, nil); !errors.Is(err, failure) {
		t.Errorf("the method's own failure = %v, want %v unchanged", err, failure)
	}
}
