// Tests of reading which organization a stored device belongs to.
package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// TestEntRepository_DeviceOrganization answers the organization a device
// belongs to, zero for one in none, and ErrItemNotFound for no such device.
func TestEntRepository_DeviceOrganization(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()
	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	client.Device.Create().SetName("owned").SetType("linux_server").SetProperties(map[string]interface{}{"host": "10.0.0.1"}).SetOrganization(org).SaveX(ctx)
	client.Device.Create().SetName("loose").SetType("linux_server").SetProperties(map[string]interface{}{"host": "10.0.0.2"}).SaveX(ctx)

	reader, ok := inventory.NewEntRepository(client, inventory.NewItemFactory()).(inventory.OrganizationReader)
	if !ok {
		t.Fatal("the ent repository does not say which organization a device belongs to")
	}
	if got, err := reader.DeviceOrganization(ctx, "owned"); err != nil || got != org.ID {
		t.Errorf("owned = %d, %v; want %d", got, err, org.ID)
	}
	if got, err := reader.DeviceOrganization(ctx, "loose"); err != nil || got != 0 {
		t.Errorf("loose = %d, %v; want 0", got, err)
	}
	if _, err := reader.DeviceOrganization(ctx, "absent"); !errors.Is(err, inventory.ErrItemNotFound) {
		t.Errorf("absent = %v, want ErrItemNotFound", err)
	}
}

// TestEntRepository_AssignDeviceOrganization puts a device in no organization
// into one, leaves one already there alone, and refuses to move a device out
// of another organization.
func TestEntRepository_AssignDeviceOrganization(t *testing.T) {
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	defer client.Close()
	ctx := context.Background()
	acme := client.Organization.Create().SetName("acme").SaveX(ctx)
	other := client.Organization.Create().SetName("other").SaveX(ctx)
	client.Device.Create().SetName("loose").SetType("linux_server").SetProperties(map[string]interface{}{"host": "10.0.0.2"}).SaveX(ctx)

	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	assigner := repo.(inventory.OrganizationAssigner)
	reader := repo.(inventory.OrganizationReader)
	if err := assigner.AssignDeviceOrganization(ctx, "loose", acme.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := reader.DeviceOrganization(ctx, "loose"); got != acme.ID {
		t.Fatalf("loose is in %d, want %d", got, acme.ID)
	}
	if err := assigner.AssignDeviceOrganization(ctx, "loose", acme.ID); err != nil {
		t.Errorf("assigning the same organization again = %v", err)
	}
	if err := assigner.AssignDeviceOrganization(ctx, "loose", other.ID); !errors.Is(err, inventory.ErrCrossTenantMember) {
		t.Errorf("moving to another organization = %v, want ErrCrossTenantMember", err)
	}
	if got, _ := reader.DeviceOrganization(ctx, "loose"); got != acme.ID {
		t.Errorf("a refused move left loose in %d", got)
	}
	if err := assigner.AssignDeviceOrganization(ctx, "absent", acme.ID); !errors.Is(err, inventory.ErrItemNotFound) {
		t.Errorf("an absent device = %v, want ErrItemNotFound", err)
	}
}
