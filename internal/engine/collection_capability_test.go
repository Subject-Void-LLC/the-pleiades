package engine_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Manifest.RequiredCapabilities was documentation until this check
// existed. Register validated that each name was a capability the
// vocabulary knew, and gendocs printed it on the reference page, but
// nothing compared it against the device a task was about to run on.
//
// These tests are what make it a rule rather than a label.

// registerCapabilityMethod registers an implemented method requiring
// caps, recording whether its body ever ran. Registration is global and
// outlives the test, so each test needs its own name AND the snapshot
// below, which is what lets this package run under -count>1 at all.
func registerCapabilityMethod(t *testing.T, suffix string, caps []capability.Name, ran *bool) string {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	name := "captest." + suffix
	err := collection.Register(collection.Descriptor{
		Name: name,
		Manifest: collection.Manifest{
			Status:               collection.StatusImplemented,
			RequiredCapabilities: caps,
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

func capabilityExecutor() engine.ActionExecutor {
	return engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext)
}

// TestCheckMethodCapabilities_RefusesADeviceWithoutTheCapability is the
// whole point: a method naming a capability must not run against a device
// that does not have it.
//
// Before this, the first sign of trouble was whatever the remote shell
// said about an absent binary, which reads as a broken device rather
// than as a runbook pointed at the wrong target.
func TestCheckMethodCapabilities_RefusesADeviceWithoutTheCapability(t *testing.T) {
	ran := false
	fqcn := registerCapabilityMethod(t, "needs_systemd", []capability.Name{capability.NameSystemd}, &ran)

	device := &inventorytest.Stub{StubName: "switch1", Caps: []capability.Name{capability.NameSSHTransport}}
	task := &engine.Task{FQCN: fqcn, Params: map[string]any{}}

	_, err := capabilityExecutor().Execute(context.Background(), task, device)
	if err == nil {
		t.Fatal("expected a refusal for a device lacking the required capability")
	}
	if ran {
		t.Error("the method body ran despite the capability check failing")
	}
	// All three facts are needed to act on this: which method, which
	// capability, and which device.
	for _, want := range []string{fqcn, string(capability.NameSystemd), "switch1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestCheckMethodCapabilities_AllowsACapableDevice is the other side, and
// without it the check above could be satisfied by refusing everything.
func TestCheckMethodCapabilities_AllowsACapableDevice(t *testing.T) {
	ran := false
	fqcn := registerCapabilityMethod(t, "allows_capable", []capability.Name{capability.NameSystemd}, &ran)

	device := &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameSystemd}}
	task := &engine.Task{FQCN: fqcn, Params: map[string]any{}}

	if _, err := capabilityExecutor().Execute(context.Background(), task, device); err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if !ran {
		t.Error("the method body did not run for a device that has the capability")
	}
}

// TestCheckMethodCapabilities_ResolvesTheHierarchy proves the check uses
// HasCapability rather than comparing declared names.
//
// ServiceManagerCapable's own doc comment says it exists so a method like
// svc.restart can "target this broad capability and resolve downward on
// either OS family". A check that compared names directly would refuse
// exactly the case the parent capability was introduced for, and the
// generic svc.* methods could never run on anything.
//
// The device is the real linux.Server, not a stub, because the claim
// being tested is that a REAL device declaring the concrete child
// satisfies a method requiring the parent.
func TestCheckMethodCapabilities_ResolvesTheHierarchy(t *testing.T) {
	ran := false
	fqcn := registerCapabilityMethod(t, "needs_parent", []capability.Name{capability.NameServiceManager}, &ran)

	device, err := linux.NewServer(record.Record{ID: "l1", Name: "l1", Type: "linux_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// Precondition: the device declares the child, never the parent.
	for _, c := range device.Capabilities() {
		if c == capability.NameServiceManager {
			t.Fatal("this test is void: linux_server declares the parent directly, so nothing is being resolved")
		}
	}

	task := &engine.Task{FQCN: fqcn, Params: map[string]any{}}
	if _, err := capabilityExecutor().Execute(context.Background(), task, device); err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if !ran {
		t.Error("a device declaring SystemdCapable did not satisfy a method requiring ServiceManagerCapable")
	}
}

// TestCheckMethodCapabilities_NilDeviceIsRefused covers the targetless
// task. A method that named a capability and then ran with no device at
// all would have had its requirement silently skipped.
func TestCheckMethodCapabilities_NilDeviceIsRefused(t *testing.T) {
	ran := false
	fqcn := registerCapabilityMethod(t, "nil_device", []capability.Name{capability.NameSystemd}, &ran)

	task := &engine.Task{FQCN: fqcn, Params: map[string]any{}}
	_, err := capabilityExecutor().Execute(context.Background(), task, nil)
	if err == nil {
		t.Fatal("expected a refusal when a capability-requiring method has no target device")
	}
	if ran {
		t.Error("the method body ran with no device despite requiring a capability")
	}
	if !strings.Contains(err.Error(), "no target device") {
		t.Errorf("error = %v, want it to say there is no target device", err)
	}
}

// TestCheckMethodCapabilities_NoRequirementsStillRunsWithoutADevice keeps
// the check from overreaching. http.request requires nothing and reaches
// a URL rather than a device, so demanding a target for it would break a
// method that is correct as written.
func TestCheckMethodCapabilities_NoRequirementsStillRunsWithoutADevice(t *testing.T) {
	ran := false
	fqcn := registerCapabilityMethod(t, "no_requirements", nil, &ran)

	task := &engine.Task{FQCN: fqcn, Params: map[string]any{}}
	if _, err := capabilityExecutor().Execute(context.Background(), task, nil); err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if !ran {
		t.Error("a method requiring no capabilities did not run against a nil device")
	}
}

// TestCheckMethodCapabilities_AllRequirementsMustHold proves the check is
// an AND across the list rather than an any-of. A method naming two
// capabilities needs both, and satisfying one is not enough.
func TestCheckMethodCapabilities_AllRequirementsMustHold(t *testing.T) {
	ran := false
	fqcn := registerCapabilityMethod(t, "needs_two",
		[]capability.Name{capability.NameSystemd, capability.NamePOSIXFileSystem}, &ran)

	device := &inventorytest.Stub{StubName: "half", Caps: []capability.Name{capability.NameSystemd}}
	task := &engine.Task{FQCN: fqcn, Params: map[string]any{}}

	_, err := capabilityExecutor().Execute(context.Background(), task, device)
	if err == nil {
		t.Fatal("expected a refusal when only one of two required capabilities is present")
	}
	if ran {
		t.Error("the method body ran with only one of two required capabilities")
	}
	if !strings.Contains(err.Error(), string(capability.NamePOSIXFileSystem)) {
		t.Errorf("error = %v, want it to name the capability that is missing", err)
	}
}
