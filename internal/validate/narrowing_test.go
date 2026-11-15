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

// TestNarrowedWorld_RulesHoldTheDevicesValidateHolds: a narrowed world's
// rules hold a task to every device its target names, as validate with no
// narrowing does: a transport action naming the hidden wan router that lacks
// its capability is refused with the finding validate gives, a target the
// inventory lacks is refused as matching nothing, and neither is warned of as
// missing when the inventory has it. The blast radius is what the run reaches.
func TestNarrowedWorld_RulesHoldTheDevicesValidateHolds(t *testing.T) {
	messages := func(findings []validate.Finding) []string {
		var out []string
		for _, f := range findings {
			out = append(out, f.Message)
		}
		slices.Sort(out)
		return out
	}
	for target, matchesNothing := range map[string]bool{"wan": false, "nosuch": true} {
		world := narrowedWorld(t, "id: n\nhosts: c8kv\ntasks:\n  - name: legacy\n    ssh_exec:\n      target: "+target+"\n      command: uptime\n")
		whole := world
		whole.Resolver = nil
		narrowed, unnarrowed := messages(validate.CapabilityRule(world)), messages(validate.CapabilityRule(whole))
		if len(narrowed) == 0 || !slices.Equal(narrowed, unnarrowed) {
			t.Errorf("target %s: narrowed %v, validate %v", target, narrowed, unnarrowed)
		}
		if got := slices.ContainsFunc(narrowed, func(m string) bool { return strings.Contains(m, "matches no inventory host or tag") }); got != matchesNothing {
			t.Errorf("target %s: refused as matching nothing %v, want %v", target, got, matchesNothing)
		}
		if _, missing := world.ResolveAll(target); (len(missing) > 0) != matchesNothing {
			t.Errorf("target %s: missing %v", target, missing)
		}
	}
	world := narrowedWorld(t, "id: b\nhosts: c8kv\ntasks:\n  - noop: {}\n")
	if radius := validate.CalculateBlastRadius(world); radius.DeviceCount != 1 {
		t.Errorf("the narrowed blast radius counts %d devices, want br1 alone", radius.DeviceCount)
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
