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
