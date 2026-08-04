package validate_test

import (
	"strings"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/validate"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory/inventorytest"
)

// dagWithOneTask builds a *engine.DAG with a single flattened Nodes entry,
// mirroring the shape internal/engine's buildFromDef produces (a single
// top-level task gets the synthesized ID "tasks[0]"), without going
// through the YAML/JSON builder machinery.
func dagWithOneTask(fqcn string, target string) *engine.DAG {
	return &engine.DAG{
		ID: "t",
		Nodes: map[string]*engine.Task{
			"tasks[0]": {FQCN: fqcn, Params: map[string]interface{}{"target": target}},
		},
		Adjacency: map[string][]engine.EdgeConfig{},
	}
}

// TestCapabilityRule_ReleaseGate is Phase W3's Release Gate: pleiades
// validate rejects a runbook targeting a device that lacks the required
// capability, with a message naming the device and the capability.
func TestCapabilityRule_ReleaseGate(t *testing.T) {
	webOnly := &inventorytest.Stub{
		StubName: "webserver1",
		StubTags: []inventory.Tag{"web"},
		Caps:     []capability.Name{capability.NameSSHTransport},
	}

	world := validate.WorldView{
		Items: []inventory.InventoryItem{webOnly},
		DAG:   dagWithOneTask("ios_backup", "web"),
	}

	report := validate.Validate(world)
	if !report.HasErrors() {
		t.Fatal("expected validate to reject a task requiring CiscoIOSCapable against a device that lacks it")
	}

	msg := report.String()
	if !strings.Contains(msg, "webserver1") {
		t.Errorf("expected the message to name the device, got: %s", msg)
	}
	if !strings.Contains(msg, string(capability.NameCiscoIOS)) {
		t.Errorf("expected the message to name the missing capability, got: %s", msg)
	}
	if !strings.Contains(msg, "tasks[0]") {
		t.Errorf("expected the message to name the task's synthesized ID, got: %s", msg)
	}
}

// TestCapabilityRule_NamedTaskInMessage checks that a task's human-given
// Name, when set, is folded into the Finding's Message alongside its
// synthesized ID.
func TestCapabilityRule_NamedTaskInMessage(t *testing.T) {
	webOnly := &inventorytest.Stub{
		StubName: "webserver1",
		StubTags: []inventory.Tag{"web"},
		Caps:     []capability.Name{capability.NameSSHTransport},
	}

	dag := &engine.DAG{
		ID: "t",
		Nodes: map[string]*engine.Task{
			"tasks[0]": {
				Name:   "backup the router",
				FQCN:   "ios_backup",
				Params: map[string]interface{}{"target": "web"},
			},
		},
		Adjacency: map[string][]engine.EdgeConfig{},
	}

	world := validate.WorldView{Items: []inventory.InventoryItem{webOnly}, DAG: dag}
	report := validate.Validate(world)

	msg := report.String()
	if !strings.Contains(msg, "backup the router") {
		t.Errorf("expected the message to include the task's Name, got: %s", msg)
	}
	if !strings.Contains(msg, "tasks[0]") {
		t.Errorf("expected the message to still include the task's synthesized ID, got: %s", msg)
	}
}

func TestCapabilityRule_TableDriven(t *testing.T) {
	sshDevice := &inventorytest.Stub{
		StubName:  "sshhost",
		StubTags:  []inventory.Tag{"group-a"},
		Caps:      []capability.Name{capability.NameSSHTransport},
		StubState: inventory.StateActive,
	}

	cases := []struct {
		name        string
		fqcn        string
		target      string
		items       []inventory.InventoryItem
		wantFinding bool
	}{
		{"capability present via name", "ssh_exec", "sshhost", []inventory.InventoryItem{sshDevice}, false},
		{"capability present via tag", "ssh_exec", "group-a", []inventory.InventoryItem{sshDevice}, false},
		{"capability missing", "ios_backup", "sshhost", []inventory.InventoryItem{sshDevice}, true},
		{"target resolves to nothing", "ssh_exec", "does-not-exist", []inventory.InventoryItem{sshDevice}, true},
		{"action has no capability requirement", "noop", "sshhost", []inventory.InventoryItem{sshDevice}, false},
		{"no target set", "ssh_exec", "", []inventory.InventoryItem{sshDevice}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			world := validate.WorldView{Items: tc.items, DAG: dagWithOneTask(tc.fqcn, tc.target)}
			report := validate.Validate(world)
			if report.HasErrors() != tc.wantFinding {
				t.Errorf("Validate() findings=%v, want findings=%v (report: %s)", report.HasErrors(), tc.wantFinding, report.String())
			}
		})
	}
}
