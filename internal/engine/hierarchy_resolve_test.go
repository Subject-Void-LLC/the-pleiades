package engine_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// routeExtract pulls a string "route" key out of a layer's Properties,
// the shape a real bastion/hop-chain route setting will actually take.
func routeExtract(props map[string]interface{}) (string, bool) {
	v, ok := props["route"]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func TestResolveHierarchy_NoLayersReturnsBase(t *testing.T) {
	result := engine.ResolveHierarchy("system-default", nil, routeExtract)
	if result.Value != "system-default" {
		t.Errorf("Value = %q, want the base unchanged", result.Value)
	}
	if len(result.Layers) != 0 {
		t.Errorf("Layers = %v, want none consulted", result.Layers)
	}
}

func TestResolveHierarchy_MostSpecificLayerWins(t *testing.T) {
	ancestry := []inventory.HierarchyLayer{
		{Name: "prod-inventory", Properties: map[string]interface{}{"route": "via-inventory-bastion"}},
		{Name: "region-group", Properties: map[string]interface{}{"route": "via-region-bastion"}},
		{Name: "rack-group", Properties: map[string]interface{}{"route": "via-rack-bastion"}},
	}

	result := engine.ResolveHierarchy("system-default", ancestry, routeExtract)
	if result.Value != "via-rack-bastion" {
		t.Errorf("Value = %q, want the most specific (last) layer's value", result.Value)
	}
}

// TestResolveHierarchy_LayerWithNoOpinionInheritsRatherThanBlanks proves the
// sparse-field rule: a layer whose Properties has no "route" key at all
// (a group that exists for some other reason, with nothing to say about
// bastion routing) must not reset the value a less specific ancestor
// already configured.
func TestResolveHierarchy_LayerWithNoOpinionInheritsRatherThanBlanks(t *testing.T) {
	ancestry := []inventory.HierarchyLayer{
		{Name: "region-group", Properties: map[string]interface{}{"route": "via-region-bastion"}},
		{Name: "unrelated-group", Properties: map[string]interface{}{"unrelated-setting": "irrelevant"}},
		{Name: "device-group", Properties: nil},
	}

	result := engine.ResolveHierarchy("system-default", ancestry, routeExtract)
	if result.Value != "via-region-bastion" {
		t.Errorf("Value = %q, want the region layer's value to survive through two layers with no opinion", result.Value)
	}
	// Only the layer that actually had an opinion is recorded, so a
	// caller inspecting Layers sees what really contributed, not every
	// ancestor visited.
	if len(result.Layers) != 1 || result.Layers[0] != "region-group" {
		t.Errorf("Layers = %v, want exactly [region-group]", result.Layers)
	}
}

func TestResolveHierarchy_NoLayerHasAnOpinionReturnsBase(t *testing.T) {
	ancestry := []inventory.HierarchyLayer{
		{Name: "region-group", Properties: map[string]interface{}{"other-setting": "x"}},
		{Name: "rack-group", Properties: nil},
	}

	result := engine.ResolveHierarchy("system-default", ancestry, routeExtract)
	if result.Value != "system-default" {
		t.Errorf("Value = %q, want the base when no layer has an opinion", result.Value)
	}
	if len(result.Layers) != 0 {
		t.Errorf("Layers = %v, want none: nothing actually contributed", result.Layers)
	}
}
