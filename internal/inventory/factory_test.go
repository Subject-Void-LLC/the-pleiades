package inventory_test

import (
	"context"
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/ent/enttest"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

func TestFactoryHydration(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ent?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()

	// Release Gate: Save a device to SQLite
	dev := client.Device.Create().
		SetName("cisco-lab-1").
		SetProperties(map[string]interface{}{
			"type":            "cisco_router",
			"host":            "10.10.10.1",
			"port":            22,
			"ios_version":     "17.3.2",
			"netconf_enabled": true,
		}).
		SaveX(ctx)

	// Pull the raw row via ORM
	queriedDev := client.Device.GetX(ctx, dev.ID)

	// Hydrate using ItemFactory
	factory := inventory.NewItemFactory()
	item, err := factory.Build(queriedDev)
	if err != nil {
		t.Fatalf("factory failed to build item: %v", err)
	}

	// Type-assert it to CiscoIOSCapable
	ciscoDevice, ok := item.(inventory.CiscoIOSCapable)
	if !ok {
		t.Fatalf("hydrated item does not implement CiscoIOSCapable")
	}

	if ciscoDevice.IOSVersion() != "17.3.2" {
		t.Errorf("expected IOS version 17.3.2, got %s", ciscoDevice.IOSVersion())
	}

	// Also check SSHTransportCapable
	sshDev, ok := item.(inventory.SSHTransportCapable)
	if !ok {
		t.Fatalf("hydrated item does not implement SSHTransportCapable")
	}

	if sshDev.SSHHost() != "10.10.10.1" {
		t.Errorf("expected SSH host 10.10.10.1, got %s", sshDev.SSHHost())
	}
}
