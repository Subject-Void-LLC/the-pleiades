// This file holds the ent-backed SetStore's UpdateWith to its contract: a
// fold runs on the Set as it stands under the row's lock, two updates of one
// Set never interleave, and the result is held to the rules a create is.
//
// The concurrency test runs internal/storage/rowlocktest's scenario, which
// that package also runs on PostgreSQL, on a real SQLite file opened through
// ent.OpenEmbedded, the Controller's own path. A shared-cache in-memory database answers a
// contending writer with SQLITE_LOCKED, which no busy timeout retries, so a
// test on one would pass or fail for a reason unrelated to the lock.
package inventory_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/storage/rowlocktest"
)

// openSetStoreFile opens a SetStore over a fresh SQLite file.
func openSetStoreFile(t *testing.T) (inventory.SetStore, *ent.Client) {
	t.Helper()
	client, err := ent.OpenEmbedded(context.Background(), filepath.Join(t.TempDir(), "sets.db"))
	if err != nil {
		t.Fatalf("OpenEmbedded() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return inventory.NewEntSetStore(client), client
}

// TestEntSetStore_UpdateWithFoldsOverTheOtherUpdatesWrite is the lost update
// the lock exists for. One update, a rename, is held inside its fold while a
// second, which removes a device (revoking it from every share of the
// inventory), starts. The second must not run its fold until the first has
// committed, and must fold over the name the first wrote, so the result
// keeps both changes. Folding over a copy read before the first committed
// would write the old name back over the first's.
func TestEntSetStore_UpdateWithFoldsOverTheOtherUpdatesWrite(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	keep, revoke := newDevice(t, client, "keep", org), newDevice(t, client, "revoke", org)
	created, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: org, DeviceIDs: []int{keep, revoke}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var sawName string
	rowlocktest.SecondFoldWaits(t,
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) {
				onFold()
				s.Name = "edge-routers"
				return s, nil
			})
			return err
		},
		func(onFold func()) error {
			_, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) {
				onFold()
				sawName = s.Name
				s.DeviceIDs = []int{keep}
				return s, nil
			})
			return err
		})

	if sawName != "edge-routers" {
		t.Errorf("the second fold saw name %q, want the first update's edge-routers", sawName)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "edge-routers" || !slices.Equal(got.DeviceIDs, []int{keep}) {
		t.Errorf("after both updates: name %q devices %v, want edge-routers and [%d]", got.Name, got.DeviceIDs, keep)
	}
}

// TestEntSetStore_UpdateWithHoldsTheResultToTheCreateRules covers what
// UpdateWith refuses and keeps whatever the fold returns: a blank name, a
// member that does not exist or belongs to another tenant, and a fold's own
// error, each leaving the Set as it was; and a moved organization, owner or
// id, which are kept as stored.
func TestEntSetStore_UpdateWithHoldsTheResultToTheCreateRules(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	org, other := newOrg(t, client, "acme"), newOrg(t, client, "globex")
	mine, theirs := newDevice(t, client, "mine", org), newDevice(t, client, "theirs", other)
	created, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: org, Owner: "alice", DeviceIDs: []int{mine}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	refusedFold := errors.New("refused by the fold")

	cases := map[string]struct {
		change  func(*inventory.Set)
		foldErr error
		want    error
	}{
		"a blank name":          {change: func(s *inventory.Set) { s.Name = " \t" }, want: inventory.ErrSetNameRequired},
		"an unknown device":     {change: func(s *inventory.Set) { s.DeviceIDs = []int{mine, 999999} }, want: inventory.ErrUnknownMember},
		"an unknown group":      {change: func(s *inventory.Set) { s.GroupIDs = []int{999999} }, want: inventory.ErrUnknownMember},
		"another tenant device": {change: func(s *inventory.Set) { s.DeviceIDs = []int{theirs} }, want: inventory.ErrCrossTenantMember},
		"the fold's own error":  {foldErr: refusedFold, want: refusedFold},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) {
				if tc.foldErr != nil {
					return inventory.Set{}, tc.foldErr
				}
				tc.change(&s)
				return s, nil
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("UpdateWith() error = %v, want %v", err, tc.want)
			}
			if got, _ := store.Get(ctx, created.ID); got.Name != "edge" || !slices.Equal(got.DeviceIDs, []int{mine}) {
				t.Errorf("a refused update changed the set: %+v", got)
			}
		})
	}

	got, err := store.UpdateWith(ctx, created.ID, func(s inventory.Set) (inventory.Set, error) {
		s.ID, s.OrganizationID, s.Owner = 4242, other, "mallory"
		s.DeviceIDs = []int{mine, mine}
		return s, nil
	})
	if err != nil {
		t.Fatalf("UpdateWith() error = %v", err)
	}
	if got.ID != created.ID || got.OrganizationID != org || got.Owner != "alice" || !slices.Equal(got.DeviceIDs, []int{mine}) {
		t.Errorf("UpdateWith() = %+v, want the id, organization and owner kept and the device once", got)
	}
}

// TestEntSetStore_UpdateWithAMissingSetIsNotFound never calls the fold.
func TestEntSetStore_UpdateWithAMissingSetIsNotFound(t *testing.T) {
	t.Parallel()
	store, _ := openSetStoreFile(t)
	called := false
	_, err := store.UpdateWith(context.Background(), 4242, func(s inventory.Set) (inventory.Set, error) {
		called = true
		return s, nil
	})
	if !errors.Is(err, inventory.ErrSetNotFound) || called {
		t.Errorf("UpdateWith() error = %v, fold called %v; want ErrSetNotFound and no call", err, called)
	}
}

// TestEntSetStore_CreateRefusesWhatItCannotSave is Create's share: a blank
// name, no organization or an unknown one, and an unknown member are typed
// refusals, never a constraint error read as a name already taken; a
// repeated member is stored once.
func TestEntSetStore_CreateRefusesWhatItCannotSave(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	device := newDevice(t, client, "edge-1", org)

	cases := map[string]struct {
		set  inventory.Set
		want error
	}{
		"a blank name":            {inventory.Set{Name: "  ", OrganizationID: org}, inventory.ErrSetNameRequired},
		"no organization":         {inventory.Set{Name: "edge"}, inventory.ErrSetOrganizationRequired},
		"an unknown organization": {inventory.Set{Name: "edge", OrganizationID: 999999}, inventory.ErrUnknownOrganization},
		"an unknown device":       {inventory.Set{Name: "edge", OrganizationID: org, DeviceIDs: []int{999999}}, inventory.ErrUnknownMember},
		"an unknown group":        {inventory.Set{Name: "edge", OrganizationID: org, GroupIDs: []int{999999}}, inventory.ErrUnknownMember},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Create(ctx, tc.set); !errors.Is(err, tc.want) {
				t.Errorf("Create() error = %v, want %v", err, tc.want)
			}
		})
	}

	created, err := store.Create(ctx, inventory.Set{Name: "edge", OrganizationID: org, DeviceIDs: []int{device, device}})
	if err != nil {
		t.Fatalf("Create() with a repeated device error = %v", err)
	}
	if !slices.Equal(created.DeviceIDs, []int{device}) {
		t.Errorf("devices = %v, want [%d] once", created.DeviceIDs, device)
	}
}

// TestEntSetStore_UpdateWithLeavesAStoredBlankNameAlone is LESSONS 415 and
// 422's rule: an older build's PATCH stored a name of spaces, and an update
// that only revokes a device must not be refused for the name it leaves as
// it is. Changing the name, to anything blank, is still refused.
func TestEntSetStore_UpdateWithLeavesAStoredBlankNameAlone(t *testing.T) {
	t.Parallel()
	store, client := openSetStoreFile(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	keep, revoke := newDevice(t, client, "keep", org), newDevice(t, client, "revoke", org)
	// Written past the store, as the old PATCH route could.
	id := client.Inventory.Create().SetName("   ").SetOrganizationID(org).AddDeviceIDs(keep, revoke).SaveX(ctx).ID

	got, err := store.UpdateWith(ctx, id, func(s inventory.Set) (inventory.Set, error) {
		s.DeviceIDs = []int{keep}
		return s, nil
	})
	if err != nil {
		t.Fatalf("UpdateWith() revoking a device = %v, want it applied", err)
	}
	if !slices.Equal(got.DeviceIDs, []int{keep}) {
		t.Errorf("devices = %v, want [%d]", got.DeviceIDs, keep)
	}
	if _, err := store.UpdateWith(ctx, id, func(s inventory.Set) (inventory.Set, error) {
		s.Name = "\t"
		return s, nil
	}); !errors.Is(err, inventory.ErrSetNameRequired) {
		t.Errorf("UpdateWith() to another blank name = %v, want ErrSetNameRequired", err)
	}
}
