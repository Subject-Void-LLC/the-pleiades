// Tests for the engine's run-time transport gate, the half of
// validate's TransportRule that runs just before a method does.
package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerTransportMethod registers an implemented method that needs
// CommandExecCapable and reaches its device over transports, recording
// whether its body ran: the shape of exec.command.
func registerTransportMethod(t *testing.T, suffix string, transports []string, ran *bool) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	name := "transporttest." + suffix
	err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:               collection.StatusImplemented,
			SupportedTransports:  transports,
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			Reversibility:        collection.Reversibility{Notes: "a test fixture that changes nothing"},
		},
		Invoke: func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			*ran = true
			return collection.Result{}, nil
		},
	})
	if err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
	return name
}

// TestCollectionTransports_RefusesADeviceTheMethodCannotReach is the
// run-time half of validate's TransportRule, and the reason
// WindowsShellCapable can be a child of CommandExecCapable: a Windows
// server has the capability an SSH-only method requires, and only this
// check stops the method running against it, in check mode as well as
// for real.
func TestCollectionTransports_RefusesADeviceTheMethodCannotReach(t *testing.T) {
	ran := false
	fqcn := registerTransportMethod(t, "ssh_only", []string{capability.TransportSSH}, &ran)

	windows := &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsShell, capability.NameCommandExec}}
	task := &engine.Task{FQCN: fqcn, Params: map[string]any{}}

	for mode, run := range map[string]func() (engine.ActionResult, error){
		"execute": func() (engine.ActionResult, error) {
			return capabilityExecutor().Execute(context.Background(), task, windows)
		},
		"check": func() (engine.ActionResult, error) {
			return capabilityExecutor().(engine.CheckExecutor).Check(context.Background(), task, windows)
		},
	} {
		_, err := run()
		if err == nil || !strings.Contains(err.Error(), "reaches its device over ssh") || !strings.Contains(err.Error(), `"win1" reaches only winrm`) {
			t.Errorf("%s: err = %v, want the transport refusal", mode, err)
		}
	}
	if ran {
		t.Error("the method body ran against a device it cannot reach")
	}
}

// TestCollectionTransports_AllowsAReachableDevice is the other side:
// without it the check above could be refusing everything.
func TestCollectionTransports_AllowsAReachableDevice(t *testing.T) {
	ran := false
	fqcn := registerTransportMethod(t, "either", []string{capability.TransportSSH, capability.TransportWinRM}, &ran)

	windows := &inventorytest.Stub{StubName: "win1", Caps: []capability.Name{capability.NameWinRM, capability.NameCommandExec}}
	if _, err := capabilityExecutor().Execute(context.Background(), &engine.Task{FQCN: fqcn, Params: map[string]any{}}, windows); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !ran {
		t.Error("the method did not run on a device reaching one of its transports")
	}
}
