package inventory_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestInventoryItemCompliance hydrates a real concrete type through the
// factory and exercises the full base contract: identity, capability
// declaration, and the versioned AddInfo/History trail. If CiscoRouter
// stopped implementing pkginventory.InventoryItem, the var declaration
// below would fail to compile; that is the compliance check, not the
// assertions that follow.
func TestInventoryItemCompliance(t *testing.T) {
	factory := inventory.NewItemFactory()

	item, err := factory.Build(record.Record{
		ID:   pkginventory.DeviceID("switch-1"),
		Name: "switch-1",
		Type: "cisco_router",
		Properties: map[string]pkginventory.PropertyValue{
			"host": "10.0.0.1",
		},
		Tags: []pkginventory.Tag{"core"},
	})
	if err != nil {
		t.Fatalf("failed to build item: %v", err)
	}

	var _ pkginventory.InventoryItem = item

	if item.ID() != "switch-1" {
		t.Errorf("expected ID 'switch-1', got '%s'", item.ID())
	}
	if item.Name() != "switch-1" {
		t.Errorf("expected Name 'switch-1', got '%s'", item.Name())
	}
	if item.State() != pkginventory.StateDiscovered {
		t.Errorf("expected zero-value state Discovered, got %s", item.State())
	}

	// AddInfo records a Revision and bumps Version.
	if err := item.AddInfo("rack", "R42", false); err != nil {
		t.Fatalf("AddInfo failed: %v", err)
	}
	if item.Version() != 1 {
		t.Errorf("expected version 1 after one AddInfo, got %d", item.Version())
	}
	if len(item.History()) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(item.History()))
	}

	// AddInfo without overwrite on an existing key is rejected.
	if err := item.AddInfo("rack", "R43", false); err == nil {
		t.Error("expected AddInfo to reject overwrite=false on an existing key")
	}

	if !item.HasCapability(capability.NameSSHTransport) {
		t.Error("cisco_router should declare SSHTransportCapable")
	}
	if item.HasCapability(capability.NameLinux) {
		t.Error("cisco_router should not declare LinuxCapable")
	}
}
