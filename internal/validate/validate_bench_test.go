package validate_test

import (
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// BenchmarkValidateFullInventory measures validation time against a
// 10,000-host inventory, the same order of magnitude Phase 14's dispatch
// release gate uses, so the two numbers are comparable.
func BenchmarkValidateFullInventory(b *testing.B) {
	const hostCount = 10000

	items := make([]inventory.InventoryItem, hostCount)
	nodes := make(map[string]*engine.Task, hostCount)
	for i := 0; i < hostCount; i++ {
		name := fmt.Sprintf("host-%d", i)
		items[i] = &inventorytest.Stub{
			StubID:    inventory.DeviceID(name),
			StubName:  name,
			Caps:      []capability.Name{capability.NameSSHTransport},
			StubState: inventory.StateActive,
		}
		nodes[fmt.Sprintf("tasks[%d]", i)] = &engine.Task{
			Name:   name,
			FQCN:   "ssh_exec",
			Params: map[string]interface{}{"target": name},
		}
	}

	dag := &engine.DAG{ID: "bench", Nodes: nodes, Adjacency: map[string][]engine.EdgeConfig{}}
	world := validate.WorldView{Items: items, DAG: dag}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validate.Validate(world)
	}
}

// BenchmarkCalculateBlastRadiusFullInventory measures BlastRadius
// computation against the same 10,000-host inventory shape
// BenchmarkValidateFullInventory uses, so the two numbers stay comparable.
func BenchmarkCalculateBlastRadiusFullInventory(b *testing.B) {
	const hostCount = 10000

	items := make([]inventory.InventoryItem, hostCount)
	nodes := make(map[string]*engine.Task, hostCount)
	for i := 0; i < hostCount; i++ {
		name := fmt.Sprintf("host-%d", i)
		items[i] = &inventorytest.Stub{
			StubID:    inventory.DeviceID(name),
			StubName:  name,
			Props:     map[string]inventory.PropertyValue{"tier": "prod"},
			Caps:      []capability.Name{capability.NameSSHTransport},
			StubState: inventory.StateActive,
		}
		nodes[fmt.Sprintf("tasks[%d]", i)] = &engine.Task{
			Name:   name,
			FQCN:   "ssh_exec",
			Params: map[string]interface{}{"target": name},
		}
	}

	dag := &engine.DAG{ID: "bench", Nodes: nodes, Adjacency: map[string][]engine.EdgeConfig{}}
	world := validate.WorldView{Items: items, DAG: dag}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validate.CalculateBlastRadius(world)
	}
}
