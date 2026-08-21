package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers Repository.GroupAncestry against a real database, per
// RULE 0: the subject is a graph traversal (BFS over Group.parents, plus
// the inventories reached along the way) and a claim about SQL query
// results, neither of which a mock could stand in for.

// ancestryFixture opens a fresh in-memory database and returns an
// entRepository over it, plus the raw client the tests need to build
// group/inventory graphs no management surface exists for yet.
func ancestryFixture(t *testing.T) (inventory.Repository, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:ancestry%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	return inventory.NewEntRepository(client, inventory.NewItemFactory()), client
}

func TestGroupAncestry_DeviceInNoGroupOrInventoryReturnsNil(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	client.Device.Create().SetName("lonely").SetType("linux_server").SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "lonely")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if layers != nil {
		t.Fatalf("layers = %+v, want nil for a device in nothing", layers)
	}
}

func TestGroupAncestry_UnknownDeviceReturnsErrItemNotFound(t *testing.T) {
	repo, _ := ancestryFixture(t)

	_, err := repo.GroupAncestry(context.Background(), "does-not-exist")
	if !errors.Is(err, inventory.ErrItemNotFound) {
		t.Fatalf("err = %v, want it to wrap ErrItemNotFound", err)
	}
}

func TestGroupAncestry_OneDirectGroupIsOneLayer(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	dev := client.Device.Create().SetName("dev").SetType("linux_server").SaveX(ctx)
	client.Group.Create().
		SetName("edge").
		SetProperties(map[string]interface{}{"route": "via-edge-bastion"}).
		AddDevices(dev).
		SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %+v, want exactly 1", layers)
	}
	if layers[0].Name != "edge" {
		t.Errorf("layers[0].Name = %q, want %q", layers[0].Name, "edge")
	}
	if layers[0].Properties["route"] != "via-edge-bastion" {
		t.Errorf("layers[0].Properties = %+v, want route=via-edge-bastion", layers[0].Properties)
	}
}

// TestGroupAncestry_ParentGroupIsLessSpecificThanDirectGroup proves the
// ordering claim: a device's direct group is more specific than that
// group's own parent, so the parent must come first (least specific) and
// the direct group last (most specific).
func TestGroupAncestry_ParentGroupIsLessSpecificThanDirectGroup(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	parent := client.Group.Create().
		SetName("region").
		SetProperties(map[string]interface{}{"route": "via-region-bastion"}).
		SaveX(ctx)

	dev := client.Device.Create().SetName("dev").SetType("linux_server").SaveX(ctx)
	client.Group.Create().
		SetName("rack").
		SetProperties(map[string]interface{}{"route": "via-rack-bastion"}).
		AddDevices(dev).
		AddParents(parent).
		SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("layers = %+v, want exactly 2", layers)
	}
	if layers[0].Name != "region" || layers[1].Name != "rack" {
		t.Fatalf("layers = [%s, %s], want [region, rack] (least to most specific)", layers[0].Name, layers[1].Name)
	}
}

// TestGroupAncestry_InventoryAlwaysOutranksEveryGroup proves an Inventory
// is always the least specific layer, regardless of how many group levels
// separate the device from it: it is emitted before every group layer,
// even a group closer to the inventory in the graph than the device's own
// direct group.
func TestGroupAncestry_InventoryAlwaysOutranksEveryGroup(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	inv := client.Inventory.Create().
		SetName("prod").
		SetOrganizationID(org.ID).
		SetProperties(map[string]interface{}{"route": "via-inventory-bastion"}).
		SaveX(ctx)

	parent := client.Group.Create().
		SetName("region").
		AddInventories(inv).
		SaveX(ctx)

	dev := client.Device.Create().SetName("dev").SetType("linux_server").SaveX(ctx)
	client.Group.Create().
		SetName("rack").
		AddDevices(dev).
		AddParents(parent).
		SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("layers = %+v, want exactly 3", layers)
	}
	if layers[0].Name != "prod" {
		t.Fatalf("layers[0] = %q, want the inventory %q to be least specific of all", layers[0].Name, "prod")
	}
	if layers[1].Name != "region" || layers[2].Name != "rack" {
		t.Fatalf("layers[1:] = [%s, %s], want [region, rack]", layers[1].Name, layers[2].Name)
	}
}

// TestGroupAncestry_DirectInventoryAttachmentIsALayer proves the
// "ungrouped hosts" case: a device attached to an Inventory with no
// intervening group still gets that inventory's layer.
func TestGroupAncestry_DirectInventoryAttachmentIsALayer(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	inv := client.Inventory.Create().
		SetName("ungrouped-hosts").
		SetOrganizationID(org.ID).
		SetProperties(map[string]interface{}{"route": "via-inventory-bastion"}).
		SaveX(ctx)

	client.Device.Create().SetName("dev").SetType("linux_server").AddInventories(inv).SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 1 || layers[0].Name != "ungrouped-hosts" {
		t.Fatalf("layers = %+v, want exactly [ungrouped-hosts]", layers)
	}
}

// TestGroupAncestry_SiblingsAtTheSameDistanceOrderByName proves the
// deterministic tiebreak: two groups a device belongs to directly (same
// BFS distance, no natural order between them) come back name-ascending,
// not in map-iteration order.
func TestGroupAncestry_SiblingsAtTheSameDistanceOrderByName(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	dev := client.Device.Create().SetName("dev").SetType("linux_server").SaveX(ctx)
	client.Group.Create().SetName("zzz-group").AddDevices(dev).SaveX(ctx)
	client.Group.Create().SetName("aaa-group").AddDevices(dev).SaveX(ctx)
	client.Group.Create().SetName("mmm-group").AddDevices(dev).SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("layers = %+v, want exactly 3", layers)
	}
	want := []string{"aaa-group", "mmm-group", "zzz-group"}
	for i, w := range want {
		if layers[i].Name != w {
			t.Fatalf("layers = %v, want name-ascending order %v", layerNames(layers), want)
		}
	}
}

// TestGroupAncestry_MultiParentDAGSurfacesBothParents proves group
// nesting is treated as a DAG, not a tree: a group with two parents
// surfaces both, at the same distance, ordered by name.
func TestGroupAncestry_MultiParentDAGSurfacesBothParents(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	parentA := client.Group.Create().SetName("parent-a").SaveX(ctx)
	parentB := client.Group.Create().SetName("parent-b").SaveX(ctx)

	dev := client.Device.Create().SetName("dev").SetType("linux_server").SaveX(ctx)
	client.Group.Create().
		SetName("child").
		AddDevices(dev).
		AddParents(parentA, parentB).
		SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("layers = %+v, want exactly 3 (both parents plus the child)", layers)
	}
	if layers[0].Name != "parent-a" || layers[1].Name != "parent-b" {
		t.Fatalf("layers[:2] = [%s, %s], want [parent-a, parent-b]", layers[0].Name, layers[1].Name)
	}
	if layers[2].Name != "child" {
		t.Fatalf("layers[2] = %q, want the direct group %q last (most specific)", layers[2].Name, "child")
	}
}

// TestGroupAncestry_ReachableAtTwoDistancesUsesTheShortest proves a group
// reachable BOTH directly and through a longer path is recorded at its
// shortest (most specific) distance, not duplicated and not demoted to
// the longer path's distance.
func TestGroupAncestry_ReachableAtTwoDistancesUsesTheShortest(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	// grandparent <- parent <- direct, AND grandparent is ALSO a direct
	// group of the device. grandparent is reachable at distance 0
	// (direct) and at distance 2 (via parent), and must appear exactly
	// once, ordered as distance 0 (most specific), not distance 2.
	grandparent := client.Group.Create().SetName("grandparent").SaveX(ctx)
	parent := client.Group.Create().SetName("parent").AddParents(grandparent).SaveX(ctx)

	dev := client.Device.Create().SetName("dev").SetType("linux_server").SaveX(ctx)
	client.Group.Create().SetName("direct").AddDevices(dev).AddParents(parent).SaveX(ctx)
	grandparent.Update().AddDeviceIDs(dev.ID).SaveX(ctx)

	layers, err := repo.GroupAncestry(ctx, "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("layers = %+v, want exactly 3 (grandparent counted once, not twice)", layers)
	}

	// grandparent is reachable directly (distance 0) AND via parent
	// (distance 2). Its shortest distance, 0, is what must win: parent
	// (distance 1) is strictly less specific than a distance-0 group, so
	// parent must be emitted FIRST. If grandparent were instead recorded
	// at its longer distance-2 path, it would be even less specific than
	// parent and would come before it, which this pins against directly
	// rather than only checking the count.
	if layers[0].Name != "parent" {
		t.Fatalf("layers = %v, want parent (distance 1) first: grandparent's shortest distance (0) must win over its longer path (2)", layerNames(layers))
	}
	if !containsName(layers, "grandparent") {
		t.Fatalf("layers = %v, want grandparent present exactly once", layerNames(layers))
	}
}

func containsName(layers []inventory.HierarchyLayer, name string) bool {
	for _, l := range layers {
		if l.Name == name {
			return true
		}
	}
	return false
}

func layerNames(layers []inventory.HierarchyLayer) []string {
	names := make([]string, len(layers))
	for i, l := range layers {
		names[i] = l.Name
	}
	return names
}
