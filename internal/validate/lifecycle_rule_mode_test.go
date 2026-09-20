// Package validate_test: tests that a check, and only a check, admits a
// simulate-locked device.
package validate_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestLifecycleRule_CheckModeAdmitsOnlySimulateLocked proves the plan-time
// rule admits exactly what the executor admits in each mode: a check may
// target a simulate-locked device and nothing else it could not before,
// and a real run, including one whose WorldView never set a mode, still
// refuses it.
func TestLifecycleRule_CheckModeAdmitsOnlySimulateLocked(t *testing.T) {
	cases := []struct {
		name    string
		state   inventory.LifecycleState
		mode    collection.Mode
		refused bool
	}{
		{name: "simulate-locked, check", state: inventory.StateSimulateLocked, mode: collection.ModeCheck, refused: false},
		{name: "simulate-locked, execute", state: inventory.StateSimulateLocked, mode: collection.ModeExecute, refused: true},
		{name: "simulate-locked, mode never set", state: inventory.StateSimulateLocked, mode: "", refused: true},
		{name: "quarantined, check", state: inventory.StateQuarantined, mode: collection.ModeCheck, refused: true},
		{name: "archived, check", state: inventory.StateArchived, mode: collection.ModeCheck, refused: true},
		{name: "active, check", state: inventory.StateActive, mode: collection.ModeCheck, refused: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dev := &inventorytest.Stub{StubName: "web1", StubState: tc.state}
			world := validate.WorldView{
				Items: []inventory.InventoryItem{dev},
				DAG:   dagWithOneTask("noop", "web1"),
				Mode:  tc.mode,
			}
			findings := validate.LifecycleRule(world)
			if got := len(findings) > 0; got != tc.refused {
				t.Errorf("refused = %v, want %v (findings %+v)", got, tc.refused, findings)
			}
		})
	}
}
