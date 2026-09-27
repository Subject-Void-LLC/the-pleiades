// Tests for ItemFactory.Rebuild: an edit the item's own type would refuse
// on the next load is refused before it is saved.
package inventory_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	baseinventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// untypedItem wraps an item so that it no longer reports a device type,
// which is what Rebuild needs to rebuild it.
type untypedItem struct{ baseinventory.InventoryItem }

func TestItemFactoryRebuild(t *testing.T) {
	factory := inventory.NewItemFactory()
	item, err := factory.Build(record.Record{
		Name: "vengeance", Type: "windows_server",
		Properties: map[string]interface{}{"host": "10.0.0.1", "virtualbox": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := factory.Rebuild(item); err != nil {
		t.Fatalf("an unchanged item was refused: %v", err)
	}

	// AddInfo writes the property without the type seeing it; Rebuild is
	// where windows_server gets to refuse a relative VBoxManage path.
	if err := item.AddInfo("vboxmanage_path", "VBoxManage.exe", true); err != nil {
		t.Fatal(err)
	}
	if err := factory.Rebuild(item); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("a relative vboxmanage_path: %v, want the type's refusal", err)
	}

	if err := factory.Rebuild(untypedItem{item}); err == nil || !strings.Contains(err.Error(), "device type") {
		t.Fatalf("an item with no device type: %v", err)
	}
}
