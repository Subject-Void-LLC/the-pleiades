package inventory_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	baseinventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// TestNewItemFactoryWithConstructors_ScopedIndependently proves a factory
// built from an explicit subset is genuinely independent of the
// registry-fed default: it sees only what it was given, never the built-in
// types record.Types happens to also hold in the same test binary.
func TestNewItemFactoryWithConstructors_ScopedIndependently(t *testing.T) {
	fakeCalled := false
	factory := inventory.NewItemFactoryWithConstructors(map[string]record.Constructor{
		"fake_type": func(rec record.Record) (baseinventory.InventoryItem, error) {
			fakeCalled = true
			return nil, nil
		},
	})

	if _, err := factory.Build(record.Record{Name: "d1", Type: "fake_type"}); err != nil {
		t.Fatalf("Build(fake_type): unexpected error: %v", err)
	}
	if !fakeCalled {
		t.Fatal("scoped factory did not call the constructor it was given")
	}

	if _, err := factory.Build(record.Record{Name: "d2", Type: "cisco_router"}); err == nil {
		t.Fatal("scoped factory built a device from a type it was never given (cisco_router); " +
			"it must not fall back to the registry-fed default set")
	}
}

func TestFactoryHydration(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	// Release Gate: Save a device to SQLite
	client.Device.Create().
		SetName("cisco-lab-1").
		SetType("cisco_router").
		SetProperties(map[string]interface{}{
			"host":            "10.10.10.1",
			"port":            22,
			"ios_version":     "17.3.2",
			"netconf_enabled": true,
		}).
		SaveX(ctx)

	// Hydrate through the real path: ent row -> Record -> ItemFactory. This
	// exercises the same conversion the API dispatcher relies on, not a
	// factory call constructed by hand.
	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	iter, err := repo.GetGroup(ctx, baseinventory.Selector{})
	if err != nil {
		t.Fatalf("failed to get group iterator: %v", err)
	}
	defer iter.Close()

	if !iter.Next(ctx) {
		t.Fatalf("expected at least one device, got none (iterator error: %v)", iter.Error())
	}
	item := iter.Item()

	// HasCapability must agree with the type assertion it guarantees.
	if !item.HasCapability(capability.NameCiscoIOS) {
		t.Fatalf("hydrated item does not declare CiscoIOSCapable")
	}
	ciscoDevice, ok := item.(capability.CiscoIOSCapable)
	if !ok {
		t.Fatalf("hydrated item does not implement CiscoIOSCapable")
	}
	if ciscoDevice.IOSVersion() != "17.3.2" {
		t.Errorf("expected IOS version 17.3.2, got %s", ciscoDevice.IOSVersion())
	}

	if !item.HasCapability(capability.NameSSHTransport) {
		t.Fatalf("hydrated item does not declare SSHTransportCapable")
	}
	sshDev, ok := item.(capability.SSHTransportCapable)
	if !ok {
		t.Fatalf("hydrated item does not implement SSHTransportCapable")
	}
	if sshDev.SSHHost() != "10.10.10.1" {
		t.Errorf("expected SSH host 10.10.10.1, got %s", sshDev.SSHHost())
	}
}
