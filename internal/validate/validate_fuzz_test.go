package validate_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/validate"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory/inventorytest"
)

// FuzzValidate ensures rule evaluation never panics on an arbitrary
// fqcn/target pairing against an arbitrary single-device inventory,
// including targets, actions, and device names that share no relationship
// at all.
func FuzzValidate(f *testing.F) {
	f.Add("ssh_exec", "web", "web", true)
	f.Add("ios_backup", "", "rtr1", false)
	f.Add("noop", "anything", "x", false)
	f.Add("", "", "", false)

	f.Fuzz(func(t *testing.T, fqcn, target, deviceName string, hasSSH bool) {
		var caps []capability.Name
		if hasSSH {
			caps = append(caps, capability.NameSSHTransport)
		}

		item := &inventorytest.Stub{
			StubID:   inventory.DeviceID(deviceName),
			StubName: deviceName,
			StubTags: []inventory.Tag{inventory.Tag(deviceName)},
			Caps:     caps,
		}

		dag := &engine.DAG{
			ID: "fuzz",
			Nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: fqcn, Params: map[string]interface{}{"target": target}},
			},
			Adjacency: map[string][]engine.EdgeConfig{},
		}

		world := validate.WorldView{Items: []inventory.InventoryItem{item}, DAG: dag}
		report := validate.Validate(world)
		_ = report.String() // must never panic either

		_ = validate.CalculateBlastRadius(world) // must never panic either
	})
}
