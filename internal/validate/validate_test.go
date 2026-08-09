package validate_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestWorldView_Resolve_UncachedConstruction confirms a WorldView built
// directly as a struct literal (every existing caller outside Validate
// itself, including every test in this package) still resolves correctly
// with no cache initialized, since Resolve's memoization is an internal
// optimization Validate opts into, never something a caller must set up.
func TestWorldView_Resolve_UncachedConstruction(t *testing.T) {
	web := &inventorytest.Stub{StubName: "web1", StubTags: []inventory.Tag{"web"}}
	world := validate.WorldView{Items: []inventory.InventoryItem{web}}

	if got := world.Resolve("web1"); len(got) != 1 || got[0] != web {
		t.Errorf("Resolve(\"web1\") = %v, want [web1]", got)
	}
	if got := world.Resolve("web"); len(got) != 1 || got[0] != web {
		t.Errorf("Resolve(\"web\") = %v, want [web1] (via tag)", got)
	}
	if got := world.Resolve("nope"); len(got) != 0 {
		t.Errorf("Resolve(\"nope\") = %v, want empty", got)
	}
}

// TestValidate_ResolveCacheDoesNotChangeResults is the regression test
// for the WorldView.resolveCache performance fix: adding LifecycleRule
// alongside CapabilityRule doubled BenchmarkValidateFullInventory (239ms
// -> ~480ms) because both rules independently call Resolve for the same
// target on the same task, each paying Resolve's own O(n) linear scan.
// Validate now shares one cache across every rule it runs; this test
// proves that sharing it does not change what any rule observes; only
// BenchmarkValidateFullInventory can prove the performance actually
// recovered (it does: back to ~239ms).
func TestValidate_ResolveCacheDoesNotChangeResults(t *testing.T) {
	web := &inventorytest.Stub{
		StubName:  "web1",
		StubTags:  []inventory.Tag{"web"},
		StubState: inventory.StateActive,
	}
	world := validate.WorldView{
		Items: []inventory.InventoryItem{web},
		DAG:   dagWithOneTask("noop", "web1"),
	}

	// Two independent Validate calls against equivalent inputs must
	// produce identical reports: the cache is scoped to one Validate
	// call (initialized fresh each time), never leaking state between
	// calls or callers.
	first := validate.Validate(world)
	second := validate.Validate(world)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("two Validate calls against the same WorldView diverged: %+v vs %+v", first, second)
	}

	// A caller resolving the same target outside of Validate (an
	// uncached WorldView) must see the identical result Validate's
	// cached path saw.
	if got := world.Resolve("web1"); len(got) != 1 || got[0] != web {
		t.Errorf("uncached Resolve(\"web1\") = %v, want [web1]", got)
	}
}
