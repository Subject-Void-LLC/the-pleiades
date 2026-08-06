package validate_test

import (
	"reflect"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/validate"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestCalculateBlastRadius is table-driven over the DAG.Nodes/inventory
// combination each case builds, asserting the resulting DeviceCount and
// Tiers.
func TestCalculateBlastRadius(t *testing.T) {
	cases := []struct {
		name      string
		items     []inventory.InventoryItem
		nodes     map[string]*engine.Task
		hosts     string
		wantCount int
		wantTiers []string
	}{
		{
			name:      "zero targeted devices",
			items:     nil,
			nodes:     map[string]*engine.Task{},
			wantCount: 0,
			wantTiers: nil,
		},
		{
			name: "no task sets a target",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "d1", Props: map[string]inventory.PropertyValue{"tier": "prod"}},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "noop"},
			},
			wantCount: 0,
			wantTiers: nil,
		},
		{
			name: "multiple tasks targeting the same device count it once",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "router1"},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "router1"}},
				"tasks[1]": {FQCN: "ios_backup", Params: map[string]interface{}{"target": "router1"}},
			},
			wantCount: 1,
			wantTiers: nil,
		},
		{
			name: "device with no tier property adds nothing to Tiers",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "router1"},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "router1"}},
			},
			wantCount: 1,
			wantTiers: nil,
		},
		{
			name: "multiple devices with the same tier dedupe to one entry",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "router1", Props: map[string]inventory.PropertyValue{"tier": "prod"}},
				&inventorytest.Stub{StubID: "d2", StubName: "router2", Props: map[string]inventory.PropertyValue{"tier": "prod"}},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "router1"}},
				"tasks[1]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "router2"}},
			},
			wantCount: 2,
			wantTiers: []string{"prod"},
		},
		{
			name: "distinct tiers across devices are all kept, sorted",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "router1", Props: map[string]inventory.PropertyValue{"tier": "prod"}},
				&inventorytest.Stub{StubID: "d2", StubName: "router2", Props: map[string]inventory.PropertyValue{"tier": "lab"}},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "router1"}},
				"tasks[1]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "router2"}},
			},
			wantCount: 2,
			wantTiers: []string{"lab", "prod"},
		},
		{
			name: "target resolving to nothing contributes no device",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "router1"},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "does-not-exist"}},
			},
			wantCount: 0,
			wantTiers: nil,
		},
		{
			name: "task with no target falls back to runbook-level hosts",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "router1", Props: map[string]inventory.PropertyValue{"tier": "prod"}},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "ssh_exec"},
			},
			hosts:     "router1",
			wantCount: 1,
			wantTiers: []string{"prod"},
		},
		{
			name: "task's own target overrides runbook-level hosts",
			items: []inventory.InventoryItem{
				&inventorytest.Stub{StubID: "d1", StubName: "router1", Props: map[string]inventory.PropertyValue{"tier": "prod"}},
				&inventorytest.Stub{StubID: "d2", StubName: "router2", Props: map[string]inventory.PropertyValue{"tier": "lab"}},
			},
			nodes: map[string]*engine.Task{
				"tasks[0]": {FQCN: "ssh_exec", Params: map[string]interface{}{"target": "router2"}},
			},
			hosts:     "router1",
			wantCount: 1,
			wantTiers: []string{"lab"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			world := validate.WorldView{
				Items: tc.items,
				DAG:   &engine.DAG{ID: "t", Hosts: tc.hosts, Nodes: tc.nodes, Adjacency: map[string][]engine.EdgeConfig{}},
			}

			got := validate.CalculateBlastRadius(world)
			if got.DeviceCount != tc.wantCount {
				t.Errorf("DeviceCount = %d, want %d", got.DeviceCount, tc.wantCount)
			}
			if len(got.Tiers) == 0 && len(tc.wantTiers) == 0 {
				return // both empty (nil or []string{}); reflect.DeepEqual would over-distinguish this
			}
			if !reflect.DeepEqual(got.Tiers, tc.wantTiers) {
				t.Errorf("Tiers = %v, want %v", got.Tiers, tc.wantTiers)
			}
		})
	}
}
