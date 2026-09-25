// Tests for WorldView.Resolver: validation resolving targets the way the
// executor that will run the runbook does.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// oneDevice resolves every target, and no target at all, to one device,
// the way the Runner's resolver does for a dispatch.
type oneDevice struct{ item inventory.InventoryItem }

// Resolve implements engine.TargetResolver.
func (o oneDevice) Resolve(string) []inventory.InventoryItem {
	return []inventory.InventoryItem{o.item}
}

// TestWorldView_Resolver covers the one behavior a Resolver adds: a task
// naming no target is checked against the Resolver's device, so a
// capability the device lacks is caught before the Runner runs the task.
// Without a Resolver the same task is controller-side and reaches nothing,
// as it always was on the CLI.
func TestWorldView_Resolver(t *testing.T) {
	bare := &inventorytest.Stub{StubName: "bare"} // no capabilities at all
	dag := &engine.DAG{
		ID:        "r",
		Nodes:     map[string]*engine.Task{"tasks[0]": {Name: "run", FQCN: "ssh_exec"}},
		Adjacency: map[string][]engine.EdgeConfig{},
	}

	withResolver := validate.CapabilityRule(validate.WorldView{DAG: dag, Resolver: oneDevice{bare}})
	if len(withResolver) != 1 || !strings.Contains(withResolver[0].Message, `device "bare" does not have capability`) {
		t.Errorf("with a Resolver, findings = %v, want the device's missing capability", withResolver)
	}
	if without := validate.CapabilityRule(validate.WorldView{DAG: dag, Items: []inventory.InventoryItem{bare}}); len(without) != 0 {
		t.Errorf("without a Resolver, an untargeted task reached a device: %v", without)
	}
	if radius := validate.CalculateBlastRadius(validate.WorldView{DAG: dag, Resolver: oneDevice{bare}}); radius.DeviceCount != 1 {
		t.Errorf("blast radius with a Resolver = %d devices, want 1", radius.DeviceCount)
	}
}
