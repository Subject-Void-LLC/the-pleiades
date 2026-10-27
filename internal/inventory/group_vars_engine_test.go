// Tests that the variable-name rule this package applies to inventory files
// is the engine's, which this package cannot import in its own code.
package inventory_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestCheckVarName_MatchesTheEngine holds CheckVarName and VarsProperty equal
// to the engine's CheckVarNames and DeviceVarsProperty on names that sit on
// each side of the rule.
func TestCheckVarName_MatchesTheEngine(t *testing.T) {
	for _, name := range []string{"a", "_a", "A1", "site_name", "1a", "a-b", "a b", "", "a.b", "é"} {
		mine := inv.CheckVarName(name) == nil
		theirs := engine.CheckVarNames([]string{name}) == nil
		if mine != theirs {
			t.Errorf("%q: inventory says %v, engine says %v", name, mine, theirs)
		}
	}
	if inventory.VarsProperty != engine.DeviceVarsProperty {
		t.Errorf("VarsProperty %q differs from the engine's %q", inventory.VarsProperty, engine.DeviceVarsProperty)
	}
}
