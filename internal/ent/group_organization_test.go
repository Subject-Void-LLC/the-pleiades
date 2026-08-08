package ent_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// TestGroupDeviceMembership proves the many-to-many Group<->Device edge
// this phase added: a device can belong to more than one group, and a
// group can contain more than one device, matching PLAN.md Section 3
// ("a single device can exist in us-east/prod AND databases AND
// linux-servers"). Group.devices is the owning edge; Device.groups is
// its Ref side.
func TestGroupDeviceMembership(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:groupmembership?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	web1 := client.Device.Create().SetName("web1").SetType("linux_server").SaveX(ctx)
	web2 := client.Device.Create().SetName("web2").SetType("linux_server").SaveX(ctx)

	prod := client.Group.Create().SetName("prod").AddDevices(web1, web2).SaveX(ctx)
	databases := client.Group.Create().SetName("databases").AddDevices(web1).SaveX(ctx)

	prodDevices, err := prod.QueryDevices().All(ctx)
	if err != nil {
		t.Fatalf("querying prod's devices: %v", err)
	}
	if len(prodDevices) != 2 {
		t.Fatalf("prod group has %d devices, want 2", len(prodDevices))
	}

	databasesDevices, err := databases.QueryDevices().All(ctx)
	if err != nil {
		t.Fatalf("querying databases' devices: %v", err)
	}
	if len(databasesDevices) != 1 {
		t.Fatalf("databases group has %d devices, want 1", len(databasesDevices))
	}

	// web1 belongs to both groups at once: the many-to-many claim, proven
	// from the device side too, not just the group side.
	web1Groups, err := web1.QueryGroups().All(ctx)
	if err != nil {
		t.Fatalf("querying web1's groups: %v", err)
	}
	if len(web1Groups) != 2 {
		t.Fatalf("web1 belongs to %d groups, want 2", len(web1Groups))
	}
}

// TestGroupNestingAllowsMultipleParents proves group nesting is a DAG,
// not a tree: a single group can have more than one parent group, the
// same way an Ansible inventory group can be listed under more than one
// parent's "children:" block. If this edge were declared .Unique() (a
// tree, like Device's own parent/children edge), the second AddParents
// call below would either replace the first parent or fail; neither
// happens, because it is deliberately not unique.
func TestGroupNestingAllowsMultipleParents(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:groupnesting?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	usEast := client.Group.Create().SetName("us-east").SaveX(ctx)
	webservers := client.Group.Create().SetName("webservers").SaveX(ctx)
	prodWeb := client.Group.Create().SetName("us-east-prod-web").
		AddParents(usEast, webservers).
		SaveX(ctx)

	parents, err := prodWeb.QueryParents().All(ctx)
	if err != nil {
		t.Fatalf("querying prodWeb's parents: %v", err)
	}
	if len(parents) != 2 {
		t.Fatalf("us-east-prod-web has %d parents, want 2 (us-east and webservers)", len(parents))
	}

	usEastChildren, err := usEast.QueryChildren().All(ctx)
	if err != nil {
		t.Fatalf("querying us-east's children: %v", err)
	}
	if len(usEastChildren) != 1 || usEastChildren[0].Name != "us-east-prod-web" {
		t.Fatalf("us-east's children = %+v, want exactly [us-east-prod-web]", usEastChildren)
	}

	webserversChildren, err := webservers.QueryChildren().All(ctx)
	if err != nil {
		t.Fatalf("querying webservers' children: %v", err)
	}
	if len(webserversChildren) != 1 || webserversChildren[0].Name != "us-east-prod-web" {
		t.Fatalf("webservers' children = %+v, want exactly [us-east-prod-web]", webserversChildren)
	}
}

// TestOrganizationDeviceEdgeIsOptional proves Device's Organization edge
// is genuinely optional, not merely undeclared as required by omission: a
// device created with no organization at all is valid and queryable, and
// a second device can be attached to one. Required RBAC/tenant-filtering
// behavior built on top of this edge is Phase 8's job; this phase only
// proves the substrate itself is real and connectable.
func TestOrganizationDeviceEdgeIsOptional(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:orgoptional?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	unaffiliated := client.Device.Create().SetName("unaffiliated-device").SetType("linux_server").SaveX(ctx)
	if org, err := unaffiliated.QueryOrganization().Only(ctx); err == nil {
		t.Fatalf("expected no organization for an unaffiliated device, got %+v", org)
	}

	acme := client.Organization.Create().SetName("acme").SaveX(ctx)
	affiliated := client.Device.Create().SetName("affiliated-device").SetType("linux_server").SaveX(ctx)
	if err := client.Organization.UpdateOne(acme).AddDevices(affiliated).Exec(ctx); err != nil {
		t.Fatalf("attaching device to organization: %v", err)
	}

	got, err := affiliated.QueryOrganization().Only(ctx)
	if err != nil {
		t.Fatalf("querying affiliated device's organization: %v", err)
	}
	if got.Name != "acme" {
		t.Fatalf("affiliated device's organization = %q, want acme", got.Name)
	}

	orgDevices, err := acme.QueryDevices().All(ctx)
	if err != nil {
		t.Fatalf("querying acme's devices: %v", err)
	}
	if len(orgDevices) != 1 || orgDevices[0].Name != "affiliated-device" {
		t.Fatalf("acme's devices = %+v, want exactly [affiliated-device]", orgDevices)
	}
}
