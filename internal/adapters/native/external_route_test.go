// Package native: tests that an external Collection's method runs from the
// Runner's own process rather than a re-executed child.
package native

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestIPCCollectionExecutor_ExternalMethodsRunInThisProcess proves the
// Runner's route for an external Collection's method: a descriptor with a
// Provider (which only the loader sets) runs from this process, never
// through a re-executed child, since its own function already starts the
// external program. The executor is given a child binary that does not
// exist, so any attempt at the child route fails loudly. The method
// receives the payload's secrets, the credential the Controller resolved
// at fan-out, and the context holding them is zeroed once it returns.
//
// The control is the same method with no Provider: it takes the child
// route, whose fresh process never registered it, and fails there. That is
// what shows the route, not luck, decided where the external method ran.
func TestIPCCollectionExecutor_ExternalMethodsRunInThisProcess(t *testing.T) {
	var seen map[string]string
	var kept sdk.RunbookContext
	method := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		seen = rc.InjectSecrets()
		kept = rc
		return collection.Result{Changed: true}, rc.SetStat("echoed", params["message"])
	}
	desc := collection.Descriptor{
		Name:     "nativeexternal.route.run",
		Manifest: collection.Manifest{Status: collection.StatusImplemented},
		Invoke:   method,
		Provider: &collection.Provider{Program: "/opt/collections/route", Digest: "sha256:00"},
	}
	device := newWireDevice(wire.DispatchPayload{DeviceName: "router1", DeviceHost: "10.0.0.1", Secrets: map[string]string{"password": "fan-out-secret"}})

	noChild := &ipcCollectionExecutor{exePath: "/nonexistent/pleiades-runner-child"}
	result, facts, err := noChild.invoke(context.Background(), desc, device, map[string]any{"message": "hello"}, collection.ModeExecute)
	if err != nil {
		t.Fatalf("an external method did not run in this process (the child route cannot work here): %v", err)
	}
	if !result.Changed || facts["echoed"] != "hello" {
		t.Errorf("result = %+v, facts = %v; want the method's own answer", result, facts)
	}
	if seen["password"] != "fan-out-secret" {
		t.Errorf("the method saw secrets %v, want the payload's", seen)
	}
	if after := kept.InjectSecrets(); len(after) != 0 {
		t.Errorf("the context still holds %d secret(s) after the method returned; it must be zeroed", len(after))
	}

	realChild, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatal(err)
	}
	builtin := desc
	builtin.Provider = nil
	_, _, err = realChild.invoke(context.Background(), builtin, device, nil, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("the same method with no Provider = %v, want it to take the child route and fail there as not registered", err)
	}
}
