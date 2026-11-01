// Tests of DeviceGroupNames against a real database: a device is in its own
// groups and every ancestor of them, a cycle is walked once, and a device in
// no group has no entry.
package inventory_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func TestDeviceGroupNames_IncludesAncestorsAndSurvivesACycle(t *testing.T) {
	repo, client := ancestryFixture(t)
	ctx := context.Background()

	inRack := client.Device.Create().SetName("in-rack").SetType("linux_server").SaveX(ctx)
	inEdge := client.Device.Create().SetName("in-edge").SetType("linux_server").SaveX(ctx)
	alone := client.Device.Create().SetName("alone").SetType("linux_server").SaveX(ctx)
	region := client.Group.Create().SetName("region").SaveX(ctx)
	dc := client.Group.Create().SetName("dc").AddParents(region).SaveX(ctx)
	client.Group.Create().SetName("rack-7").AddParents(dc).AddDevices(inRack).SaveX(ctx)
	edge := client.Group.Create().SetName("edge").AddDevices(inEdge).SaveX(ctx)
	// A cycle no management surface should allow, held here so the walk is
	// proven to end: edge and loop are each other's parent.
	loop := client.Group.Create().SetName("loop").AddParents(edge).SaveX(ctx)
	edge.Update().AddParents(loop).SaveX(ctx)

	namer, ok := repo.(inventory.GroupNamer)
	if !ok {
		t.Fatal("the ent repository does not answer group names")
	}
	got, err := namer.DeviceGroupNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][]string{
		inRack.DeviceID: {"dc", "rack-7", "region"},
		inEdge.DeviceID: {"edge", "loop"},
	} {
		if names := got[pkginventory.DeviceID(id)]; !slices.Equal(names, want) {
			t.Errorf("device %s is in %v, want %v", id, names, want)
		}
	}
	if names, ok := got[pkginventory.DeviceID(alone.DeviceID)]; ok {
		t.Errorf("a device in no group has groups %s", strings.Join(names, ","))
	}
}
