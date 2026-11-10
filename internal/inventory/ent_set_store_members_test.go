// This file holds the inventory member rules decided on 2026-11-10: an
// update checks only the members it adds, so a stored member that drifted
// into another organization no longer blocks an unrelated edit, and
// SetsForDevice never answers an inventory of another organization for a
// device that belongs to one, and Set.Selector never puts such a device in
// a job, so the drift reaches nothing.
package inventory_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// TestEntSetStore_AnUpdateChecksOnlyTheMembersItAdds covers both drifts: an
// unowned device an inventory holds is later put in another organization,
// and a group it holds later gains another organization's device. A rename
// saves over either; re-adding the drifted device after removing it is
// refused, since that is an addition.
func TestEntSetStore_AnUpdateChecksOnlyTheMembersItAdds(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	mine, theirs := newOrg(t, client, "acme"), newOrg(t, client, "globex")
	loose := newDevice(t, client, "loose", 0)
	foreign := newDevice(t, client, "their-rtr", theirs)
	group := newGroup(t, client, "edge")

	set, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: mine, GroupIDs: []int{group}, DeviceIDs: []int{loose}})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	// The drifts, written past the store as the device and group writers do.
	client.Device.UpdateOneID(loose).SetOrganizationID(theirs).ExecX(ctx)
	client.Group.UpdateOneID(group).AddDeviceIDs(foreign).ExecX(ctx)

	renamed, err := store.UpdateWith(ctx, set.ID, func(s inventory.Set) (inventory.Set, error) {
		s.Name = "edge-routers"
		return s, nil
	})
	if err != nil {
		t.Fatalf("a rename over drifted members = %v, want it saved", err)
	}
	if renamed.Name != "edge-routers" {
		t.Errorf("name = %q, want edge-routers", renamed.Name)
	}

	if _, err := store.UpdateWith(ctx, set.ID, func(s inventory.Set) (inventory.Set, error) {
		s.DeviceIDs = nil
		return s, nil
	}); err != nil {
		t.Fatalf("removing the drifted device = %v, want it saved", err)
	}
	_, err = store.UpdateWith(ctx, set.ID, func(s inventory.Set) (inventory.Set, error) {
		s.DeviceIDs = []int{loose}
		return s, nil
	})
	if !errors.Is(err, inventory.ErrCrossTenantMember) {
		t.Errorf("re-adding the drifted device = %v, want ErrCrossTenantMember", err)
	}
}

// TestEntSetStore_BothMemberChecksAlwaysRun holds that an unknown member and
// a foreign one in the same list are both counted before either is returned,
// so the answer does not show which check failed first, and that the store
// still says which it was for a log.
func TestEntSetStore_BothMemberChecksAlwaysRun(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	mine, theirs := newOrg(t, client, "acme"), newOrg(t, client, "globex")
	foreign := newDevice(t, client, "their-rtr", theirs)

	_, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: mine, DeviceIDs: []int{999999, foreign}})
	if !errors.Is(err, inventory.ErrUnknownMember) || !errors.Is(err, inventory.ErrCrossTenantMember) {
		t.Errorf("Create with an unknown and a foreign device = %v, want both sentinels", err)
	}
}

// TestEntSetStore_SetsForDeviceSkipsAnotherOrganizationsInventory is the
// grant the drift used to leave: a device in organization B is no longer
// reachable through organization A's inventory, directly or through a group,
// while an unowned device and one in the inventory's own organization are.
func TestEntSetStore_SetsForDeviceSkipsAnotherOrganizationsInventory(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	mine, theirs := newOrg(t, client, "acme"), newOrg(t, client, "globex")
	loose := newDevice(t, client, "loose", 0)
	grouped := newDevice(t, client, "grouped", 0)
	own := newDevice(t, client, "own", mine)
	group := newGroup(t, client, "edge", grouped)

	set, err := store.Create(ctx, inventory.Set{
		Name: "edge", OrganizationID: mine, GroupIDs: []int{group}, DeviceIDs: []int{loose, own},
	})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	for _, device := range []int{loose, grouped, own} {
		if got, err := store.SetsForDevice(ctx, device); err != nil || !slices.Equal(got, []int{set.ID}) {
			t.Errorf("SetsForDevice(%d) before any drift = %v, %v; want [%d]", device, got, err, set.ID)
		}
	}

	client.Device.UpdateOneID(loose).SetOrganizationID(theirs).ExecX(ctx)
	client.Device.UpdateOneID(grouped).SetOrganizationID(theirs).ExecX(ctx)
	for _, device := range []int{loose, grouped} {
		if got, err := store.SetsForDevice(ctx, device); err != nil || len(got) != 0 {
			t.Errorf("SetsForDevice(%d) after it moved to another organization = %v, %v; want none", device, got, err)
		}
	}
	if got, err := store.SetsForDevice(ctx, own); err != nil || !slices.Equal(got, []int{set.ID}) {
		t.Errorf("SetsForDevice(own) = %v, %v; want [%d]", got, err, set.ID)
	}
}

// TestSetSelector_NeverStreamsAMemberOfAnotherOrganization is the dispatch
// half of the drift: a job against an inventory streams its devices through
// Set.Selector and the repository's GetGroup, which used to read the stored
// membership raw, so a device that had since moved to another organization,
// held directly or through a group, was still targeted, with its own stored
// credential. The selector now carries the inventory's organization and the
// stream keeps only that organization's devices and unowned ones.
func TestSetSelector_NeverStreamsAMemberOfAnotherOrganization(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	mine, theirs := newOrg(t, client, "acme"), newOrg(t, client, "globex")
	loose := newDevice(t, client, "loose", 0)
	grouped := newDevice(t, client, "grouped", 0)
	own := newDevice(t, client, "own", mine)
	unowned := newDevice(t, client, "unowned", 0)
	group := newGroup(t, client, "edge", grouped)

	set, err := store.Create(ctx, inventory.Set{
		Name: "edge", OrganizationID: mine, GroupIDs: []int{group}, DeviceIDs: []int{loose, own, unowned},
	})
	if err != nil {
		t.Fatalf("Create = %v", err)
	}
	client.Device.UpdateOneID(loose).SetOrganizationID(theirs).ExecX(ctx)
	client.Device.UpdateOneID(grouped).SetOrganizationID(theirs).ExecX(ctx)

	stored, err := store.Get(ctx, set.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}
	repo := inventory.NewEntRepository(client, inventory.NewItemFactory())
	it, err := repo.GetGroup(ctx, stored.Selector())
	if err != nil {
		t.Fatalf("GetGroup = %v", err)
	}
	defer it.Close()
	var streamed []string
	for it.Next(ctx) {
		streamed = append(streamed, it.Item().Name())
	}
	if err := it.Error(); err != nil {
		t.Fatalf("streaming = %v", err)
	}
	slices.Sort(streamed)
	if want := []string{"own", "unowned"}; !slices.Equal(streamed, want) {
		t.Errorf("the inventory's job would target %v, want %v (never a device now in another organization)", streamed, want)
	}
}
