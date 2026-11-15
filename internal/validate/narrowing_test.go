// Package validate_test: a WorldView whose Resolver narrows the run (the
// CLI's --limit, a serial: batch) decides as validate, which takes no limit,
// decides: a target the narrowing hid is neither missing nor refused, a
// target the inventory lacks still is, and a device's variables count for
// every device the target names (narrowed_vars_test.go).
package validate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// hidingResolver resolves over world's items by name and tag, hiding the
// devices named in hidden, as --limit's resolver does.
type hidingResolver struct {
	world  validate.WorldView
	hidden map[string]bool
}

// Resolve implements engine.TargetResolver.
func (h hidingResolver) Resolve(target string) []inventory.InventoryItem {
	var out []inventory.InventoryItem
	for _, d := range h.world.Resolve(target) {
		if !h.hidden[d.Name()] {
			out = append(out, d)
		}
	}
	return out
}

// Unnarrowed implements engine.Narrowing.
func (h hidingResolver) Unnarrowed(target string) []inventory.InventoryItem {
	return h.world.Resolve(target)
}

// HiddenBecause implements engine.Narrowing.
func (h hidingResolver) HiddenBecause(d inventory.InventoryItem) string {
	if h.hidden[d.Name()] {
		return "outside --limit \"core1\""
	}
	return ""
}

// narrowedWorld is core1 (a hub with lan_net) and wan1 (tagged wan), both in
// the inventory, with wan1 hidden from the run, for dag.
func narrowedWorld(t *testing.T, runbook string) validate.WorldView {
	t.Helper()
	items := []inventory.InventoryItem{
		&inventorytest.Stub{StubID: "core1", StubName: "core1", StubTags: []inventory.Tag{"c8kv"}, StubState: inventory.StateActive,
			Props: map[string]any{inventory.VarsProperty: map[string]any{"role": "hub", "lan_net": "10.0.0.0/24"}}},
		&inventorytest.Stub{StubID: "br1", StubName: "br1", StubTags: []inventory.Tag{"c8kv"}, StubState: inventory.StateActive,
			Props: map[string]any{inventory.VarsProperty: map[string]any{"role": "spoke"}}},
		&inventorytest.Stub{StubID: "wan1", StubName: "wan1", StubTags: []inventory.Tag{"wan"}, StubState: inventory.StateActive},
	}
	eval, _ := engine.NewCELEvaluator()
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(runbook))
	if err != nil {
		t.Fatal(err)
	}
	whole := validate.WorldView{Items: items}
	return validate.WorldView{Items: items, DAG: dag, VariablesKnown: true,
		Resolver: hidingResolver{world: whole, hidden: map[string]bool{"wan1": true, "core1": true}}}
}

// TestNarrowedWorld_AHiddenTargetIsNotRefused: CapabilityRule's check of a
// transport action's target, and the unmatched-target warning, pass a target
// the run's narrowing hid and still refuse or warn of one the inventory lacks.
func TestNarrowedWorld_AHiddenTargetIsNotRefused(t *testing.T) {
	for target, want := range map[string]bool{"wan": false, "nosuch": true} {
		world := narrowedWorld(t, "id: n\nhosts: c8kv\ntasks:\n  - name: legacy\n    ssh_exec:\n      target: "+target+"\n      command: uptime\n")
		refused := slices.ContainsFunc(validate.CapabilityRule(world), func(f validate.Finding) bool {
			return strings.Contains(f.Message, "matches no inventory host or tag")
		})
		if refused != want {
			t.Errorf("target %s: refused as matching nothing %v, want %v", target, refused, want)
		}
		if _, missing := world.ResolveAll(target); (len(missing) > 0) != want {
			t.Errorf("target %s: missing %v", target, missing)
		}
		if world.NarrowedAway(target) == want {
			t.Errorf("target %s: NarrowedAway %v", target, !want)
		}
	}
	if (validate.WorldView{}).NarrowedAway("") {
		t.Error("an empty target was narrowed away")
	}
}

// TestWorldView_NarrowingIsItsResolversOrNone: a WorldView carries its
// Resolver's narrowing to the engine, and with a Resolver that narrows
// nothing (or none) hides nothing and resolves as it always did.
func TestWorldView_NarrowingIsItsResolversOrNone(t *testing.T) {
	world := narrowedWorld(t, "id: w\nhosts: c8kv\ntasks:\n  - noop: {}\n")
	wan1 := world.Items[2]
	if got := world.Unnarrowed("wan"); len(got) != 1 || got[0].Name() != "wan1" {
		t.Errorf("Unnarrowed(wan) = %v", got)
	}
	if why := world.HiddenBecause(wan1); why != `outside --limit "core1"` {
		t.Errorf("HiddenBecause(wan1) = %q", why)
	}
	plain := validate.WorldView{Items: world.Items}
	if got := plain.Unnarrowed("wan"); len(got) != 1 || plain.HiddenBecause(wan1) != "" {
		t.Errorf("an unnarrowed world: Unnarrowed %v, HiddenBecause %q", got, plain.HiddenBecause(wan1))
	}
	var _ engine.Narrowing = plain
}
