package inventory_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
)

// mockDevice implements inventory.InventoryItem
type mockDevice struct {
	id    string
	props map[string]interface{}
	tags  []string
}

func (m *mockDevice) ID() string {
	return m.id
}

func (m *mockDevice) Properties() map[string]interface{} {
	return m.props
}

func (m *mockDevice) Tags() []string {
	return m.tags
}

func (m *mockDevice) HasCapability(capName string) bool {
	return capName == "CiscoIOSCapable"
}

func TestInventoryItemCompliance(t *testing.T) {
	// If mockDevice does not implement inventory.InventoryItem,
	// the compiler (and LSP) will throw an error here.
	var item inventory.InventoryItem = &mockDevice{
		id:    "switch-1",
		props: map[string]interface{}{"ip": "10.0.0.1"},
		tags:  []string{"core"},
	}

	if item.ID() != "switch-1" {
		t.Errorf("expected ID 'switch-1', got '%s'", item.ID())
	}
}
