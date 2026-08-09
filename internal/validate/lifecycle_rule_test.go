package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestLifecycleRule_ReleaseGate is the plan-time half of Phase W3's
// lifecycle item: pleiades validate rejects a runbook targeting a device
// that is not Active, naming the device, its actual state, and the task.
func TestLifecycleRule_ReleaseGate(t *testing.T) {
	quarantined := &inventorytest.Stub{
		StubName:  "webserver1",
		StubState: inventory.StateQuarantined,
	}

	world := validate.WorldView{
		Items: []inventory.InventoryItem{quarantined},
		DAG:   dagWithOneTask("noop", "webserver1"),
	}

	report := validate.Validate(world)
	if !report.HasErrors() {
		t.Fatal("expected validate to reject a task targeting a quarantined device")
	}

	msg := report.String()
	for _, want := range []string{"webserver1", "quarantined", "tasks[0]"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected the message to contain %q, got: %s", want, msg)
		}
	}
}

// TestLifecycleRule_FallsBackToRunbookHosts confirms a task with no
// params.target of its own is checked against the runbook-level hosts:
// default (engine.TaskTarget's default/override contract), and that a
// task's own target still overrides it.
func TestLifecycleRule_FallsBackToRunbookHosts(t *testing.T) {
	quarantined := &inventorytest.Stub{StubName: "webserver1", StubState: inventory.StateQuarantined}
	active := &inventorytest.Stub{StubName: "router1", StubState: inventory.StateActive}

	dag := &engine.DAG{
		ID:    "t",
		Hosts: "webserver1",
		Nodes: map[string]*engine.Task{
			"tasks[0]": {FQCN: "noop"}, // no params.target: falls back to Hosts
			"tasks[1]": {FQCN: "noop", Params: map[string]interface{}{"target": "router1"}},
		},
		Adjacency: map[string][]engine.EdgeConfig{},
	}

	world := validate.WorldView{Items: []inventory.InventoryItem{quarantined, active}, DAG: dag}
	report := validate.Validate(world)

	if !report.HasErrors() {
		t.Fatal("expected a finding for the task that fell back to the quarantined runbook default")
	}
	msg := report.String()
	if !strings.Contains(msg, "webserver1") {
		t.Errorf("expected the message to name the device reached via the runbook-level hosts: default, got: %s", msg)
	}
	if strings.Contains(msg, "tasks[1]") {
		t.Errorf("expected the task with its own target overriding hosts: to pass, but it was flagged: %s", msg)
	}
}

func TestLifecycleRule_TableDriven(t *testing.T) {
	cases := []struct {
		name        string
		state       inventory.LifecycleState
		target      string
		wantFinding bool
	}{
		{"active device is allowed", inventory.StateActive, "dev", false},
		{"discovered device is rejected", inventory.StateDiscovered, "dev", true},
		{"quarantined device is rejected", inventory.StateQuarantined, "dev", true},
		{"onboarding device is rejected", inventory.StateOnboarding, "dev", true},
		{"simulate-locked device is rejected", inventory.StateSimulateLocked, "dev", true},
		{"unreachable device is rejected", inventory.StateUnreachable, "dev", true},
		{"decommissioning device is rejected", inventory.StateDecommissioning, "dev", true},
		{"archived device is rejected", inventory.StateArchived, "dev", true},
		{"no target set is unaffected regardless of state", inventory.StateArchived, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dev := &inventorytest.Stub{StubName: "dev", StubState: tc.state}
			world := validate.WorldView{
				Items: []inventory.InventoryItem{dev},
				DAG:   dagWithOneTask("noop", tc.target),
			}
			report := validate.Validate(world)
			if report.HasErrors() != tc.wantFinding {
				t.Errorf("Validate() findings=%v, want findings=%v (report: %s)", report.HasErrors(), tc.wantFinding, report.String())
			}
		})
	}
}

// TestLifecycleRule_AppliesRegardlessOfFQCN confirms the lifecycle guard,
// unlike CapabilityRule, is not gated on the task's fqcn: a non-Active
// device must never accept any real work, not only work that happens to
// declare a capability requirement.
func TestLifecycleRule_AppliesRegardlessOfFQCN(t *testing.T) {
	archived := &inventorytest.Stub{StubName: "dev", StubState: inventory.StateArchived}
	world := validate.WorldView{
		Items: []inventory.InventoryItem{archived},
		DAG:   dagWithOneTask("noop", "dev"),
	}

	report := validate.Validate(world)
	if !report.HasErrors() {
		t.Error("expected a lifecycle finding for a noop task against an archived device")
	}
}
