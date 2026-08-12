package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers the ent-backed SetStore against a real in-memory SQLite
// database, per RULE 0. The subject is a store whose whole job is what
// survives a round trip and which memberships it refuses, and neither
// question can be answered by a mock: the composite unique index, the
// cascade behaviour on delete, and the two cross-tenant queries are all
// properties of the schema rather than of the Go code above it.

// newTestSetStore opens a fresh in-memory database and returns a store over
// it alongside the raw client, which the tests need in order to create the
// organizations, groups and devices an inventory points at. There is no
// management surface for any of those yet, so the client is the only way to
// build a fixture.
func newTestSetStore(t *testing.T) (inventory.SetStore, *ent.Client) {
	t.Helper()
	// A per-test database name, so tests that run in parallel or leave rows
	// behind cannot see each other's fixtures through the shared cache.
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:setstore%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	return inventory.NewEntSetStore(client), client
}

// newOrg creates an organization and returns its id.
func newOrg(t *testing.T, client *ent.Client, name string) int {
	t.Helper()
	return client.Organization.Create().SetName(name).SaveX(context.Background()).ID
}

// newDevice creates a device, optionally owned by an organization. An orgID
// of zero leaves the edge unset, which is the single-tenant deployment shape
// the tenancy check deliberately admits.
func newDevice(t *testing.T, client *ent.Client, name string, orgID int) int {
	t.Helper()
	builder := client.Device.Create().SetName(name).SetType("linux_server")
	if orgID != 0 {
		builder = builder.SetOrganizationID(orgID)
	}
	return builder.SaveX(context.Background()).ID
}

// newGroup creates a group holding the given devices.
func newGroup(t *testing.T, client *ent.Client, name string, deviceIDs ...int) int {
	t.Helper()
	return client.Group.Create().SetName(name).AddDeviceIDs(deviceIDs...).SaveX(context.Background()).ID
}

func TestEntSetStore_CreateRoundTrips(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()

	org := newOrg(t, client, "acme")
	dev := newDevice(t, client, "rtr-01", org)
	grp := newGroup(t, client, "edge", dev)

	created, err := store.Create(ctx, inventory.Set{
		Name:           "production",
		Description:    "the real one",
		OrganizationID: org,
		Owner:          "raymond@example.com",
		GroupIDs:       []int{grp},
		DeviceIDs:      []int{dev},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created set has no id")
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "production" || got.Description != "the real one" {
		t.Errorf("name/description did not survive: %+v", got)
	}
	// Owner is the field that silently vanished when it was modelled as a
	// User edge with no writer, so it is asserted rather than assumed.
	if got.Owner != "raymond@example.com" {
		t.Errorf("owner = %q, want it persisted", got.Owner)
	}
	if got.OrganizationID != org {
		t.Errorf("organization = %d, want %d", got.OrganizationID, org)
	}
	if len(got.GroupIDs) != 1 || got.GroupIDs[0] != grp {
		t.Errorf("groups = %v, want [%d]", got.GroupIDs, grp)
	}
	if len(got.DeviceIDs) != 1 || got.DeviceIDs[0] != dev {
		t.Errorf("devices = %v, want [%d]", got.DeviceIDs, dev)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("timestamps were not populated")
	}
}

func TestEntSetStore_CreateRefusesIncompleteSets(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")

	t.Run("no name", func(t *testing.T) {
		if _, err := store.Create(ctx, inventory.Set{OrganizationID: org}); err == nil {
			t.Fatal("an unnamed set was accepted")
		}
	})

	// Refused rather than defaulted: a set belonging to no organization
	// would resolve against no organization scope, and whether that made it
	// reachable by everyone or by nobody would depend on which way the
	// resolver happened to fail.
	t.Run("no organization", func(t *testing.T) {
		if _, err := store.Create(ctx, inventory.Set{Name: "orphan"}); err == nil {
			t.Fatal("a set with no organization was accepted")
		}
	})
}

// TestEntSetStore_NameIsUniquePerOrganizationNotGlobally proves the
// composite index does what its comment claims: two tenants may both have a
// "production", and one tenant may not have two.
func TestEntSetStore_NameIsUniquePerOrganizationNotGlobally(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	acme := newOrg(t, client, "acme")
	other := newOrg(t, client, "globex")

	if _, err := store.Create(ctx, inventory.Set{Name: "production", OrganizationID: acme}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := store.Create(ctx, inventory.Set{Name: "production", OrganizationID: other}); err != nil {
		t.Fatalf("a second tenant could not use the same name: %v", err)
	}

	_, err := store.Create(ctx, inventory.Set{Name: "production", OrganizationID: acme})
	if !errors.Is(err, inventory.ErrSetExists) {
		t.Fatalf("duplicate within one organization: err = %v, want ErrSetExists", err)
	}
}

// TestEntSetStore_CreateRefusesCrossTenantMembers is the security assertion
// this store exists to make.
//
// An inventory is a grant surface: sharing one is a RoleBinding at inventory
// scope, so its membership is a permission written in a different
// vocabulary. Without this check a caller could create an inventory in their
// own organization, list another tenant's devices in it, share it with their
// own team, and be legitimately authorized against hosts nobody granted
// them, with every individual step passing its own check.
func TestEntSetStore_CreateRefusesCrossTenantMembers(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	mine := newOrg(t, client, "acme")
	theirs := newOrg(t, client, "globex")

	// Decoys first, so the foreign device's primary key is far away from the
	// count of foreign devices the refusal reports. Without them the id and
	// the count would both be 1, and the leak assertion below would be
	// reading the count while claiming to read the id.
	for i := range 4 {
		newDevice(t, client, fmt.Sprintf("decoy-%d", i), mine)
	}
	foreignDevice := newDevice(t, client, "their-rtr", theirs)
	foreignGroup := newGroup(t, client, "their-edge", foreignDevice)

	t.Run("a device from another organization", func(t *testing.T) {
		_, err := store.Create(ctx, inventory.Set{
			Name: "sneaky", OrganizationID: mine, DeviceIDs: []int{foreignDevice},
		})
		if !errors.Is(err, inventory.ErrCrossTenantMember) {
			t.Fatalf("err = %v, want ErrCrossTenantMember", err)
		}
		// The refusal reports a count and never the ids. Naming which
		// devices belong to somebody else would answer, on the very request
		// that probed for it, the question the caller was asking.
		if foreignDevice < 2 {
			t.Fatalf("fixture gave the foreign device id %d, which collides with the reported count", foreignDevice)
		}
		if msg := err.Error(); strings.Contains(msg, strconv.Itoa(foreignDevice)) {
			t.Errorf("refusal leaked the device id: %s", msg)
		}
	})

	// A group has no organization edge of its own, so its tenancy is its
	// devices'. Without the second query a group holding one foreign device
	// would smuggle it past the direct check.
	t.Run("a group holding a device from another organization", func(t *testing.T) {
		_, err := store.Create(ctx, inventory.Set{
			Name: "sneakier", OrganizationID: mine, GroupIDs: []int{foreignGroup},
		})
		if !errors.Is(err, inventory.ErrCrossTenantMember) {
			t.Fatalf("err = %v, want ErrCrossTenantMember", err)
		}
	})
}

// TestEntSetStore_CreateAdmitsDevicesWithNoOrganization proves the
// deliberate hole is actually open. A single-tenant deployment has never
// populated the device to organization edge, and refusing those devices
// would make the feature unusable for the deployments most likely to try it
// first. They belong to no tenant, so admitting them crosses no boundary.
func TestEntSetStore_CreateAdmitsDevicesWithNoOrganization(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	untenanted := newDevice(t, client, "legacy-box", 0)

	if _, err := store.Create(ctx, inventory.Set{
		Name: "mixed", OrganizationID: org, DeviceIDs: []int{untenanted},
	}); err != nil {
		t.Fatalf("an untenanted device was refused: %v", err)
	}
}

func TestEntSetStore_GetReportsAMissingSet(t *testing.T) {
	store, _ := newTestSetStore(t)

	_, err := store.Get(context.Background(), 4242)
	if !errors.Is(err, inventory.ErrSetNotFound) {
		t.Fatalf("err = %v, want ErrSetNotFound", err)
	}
}

func TestEntSetStore_List(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	acme := newOrg(t, client, "acme")
	globex := newOrg(t, client, "globex")

	mustCreate(t, store, inventory.Set{Name: "alpha", OrganizationID: acme})
	second := mustCreate(t, store, inventory.Set{Name: "beta", OrganizationID: acme})
	mustCreate(t, store, inventory.Set{Name: "gamma", OrganizationID: globex})

	t.Run("returns everything when no organization is named", func(t *testing.T) {
		// Documented behaviour, not an oversight: authorization is the
		// caller's decision, so an empty OrganizationIDs is "no restriction".
		got, err := store.List(ctx, inventory.SetQuery{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("returned %d sets, want all 3", len(got))
		}
	})

	t.Run("filters by organization", func(t *testing.T) {
		got, err := store.List(ctx, inventory.SetQuery{OrganizationIDs: []int{globex}})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 || got[0].Name != "gamma" {
			t.Fatalf("got %d sets %v, want just gamma", len(got), names(got))
		}
	})

	t.Run("pages forward from a cursor", func(t *testing.T) {
		got, err := store.List(ctx, inventory.SetQuery{After: second.ID})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 || got[0].Name != "gamma" {
			t.Fatalf("got %v, want just gamma after the cursor", names(got))
		}
	})

	t.Run("honours a limit", func(t *testing.T) {
		got, err := store.List(ctx, inventory.SetQuery{Limit: 2})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("returned %d sets, want 2", len(got))
		}
	})

	// The ceiling is the store's, never the caller's: a page size read from
	// a query parameter with no cap is a request to hold every row in memory.
	t.Run("caps an absurd limit rather than honouring it", func(t *testing.T) {
		got, err := store.List(ctx, inventory.SetQuery{Limit: 100000})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("returned %d sets, want all 3 within the cap", len(got))
		}
	})

	t.Run("searches case insensitively", func(t *testing.T) {
		got, err := store.List(ctx, inventory.SetQuery{Search: "ALPH"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 || got[0].Name != "alpha" {
			t.Fatalf("got %v, want just alpha", names(got))
		}
	})
}

// TestEntSetStore_UpdateReplacesMembershipAndKeepsTheOrganization covers the
// two decisions the update path makes. Membership is replaced rather than
// merged, because the caller submits the whole list and a merge would make
// removing the last device impossible. The organization comes from storage,
// so a submission cannot re-scope every RoleBinding pointing at this
// inventory by changing one integer.
func TestEntSetStore_UpdateReplacesMembershipAndKeepsTheOrganization(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	other := newOrg(t, client, "globex")
	first := newDevice(t, client, "rtr-01", org)
	second := newDevice(t, client, "rtr-02", org)

	set := mustCreate(t, store, inventory.Set{Name: "production", OrganizationID: org, DeviceIDs: []int{first}})

	set.Name = "renamed"
	set.DeviceIDs = []int{second}
	set.OrganizationID = other
	if err := store.Update(ctx, set); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.Get(ctx, set.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "renamed" {
		t.Errorf("name = %q, want renamed", got.Name)
	}
	if len(got.DeviceIDs) != 1 || got.DeviceIDs[0] != second {
		t.Errorf("devices = %v, want the membership replaced with [%d]", got.DeviceIDs, second)
	}
	if got.OrganizationID != org {
		t.Errorf("organization = %d, want the stored %d rather than the submitted %d", got.OrganizationID, org, other)
	}
}

func TestEntSetStore_UpdateErrors(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	mine := newOrg(t, client, "acme")
	theirs := newOrg(t, client, "globex")
	foreign := newDevice(t, client, "their-rtr", theirs)

	t.Run("missing set", func(t *testing.T) {
		err := store.Update(ctx, inventory.Set{ID: 4242, Name: "ghost"})
		if !errors.Is(err, inventory.ErrSetNotFound) {
			t.Fatalf("err = %v, want ErrSetNotFound", err)
		}
	})

	// The same guard as create, and it must be re-checked here: an inventory
	// created clean can be edited dirty, and only the write knows.
	t.Run("cross-tenant member added by an edit", func(t *testing.T) {
		set := mustCreate(t, store, inventory.Set{Name: "clean", OrganizationID: mine})
		set.DeviceIDs = []int{foreign}
		if err := store.Update(ctx, set); !errors.Is(err, inventory.ErrCrossTenantMember) {
			t.Fatalf("err = %v, want ErrCrossTenantMember", err)
		}
	})

	t.Run("rename onto a taken name", func(t *testing.T) {
		mustCreate(t, store, inventory.Set{Name: "taken", OrganizationID: mine})
		set := mustCreate(t, store, inventory.Set{Name: "free", OrganizationID: mine})
		set.Name = "taken"
		if err := store.Update(ctx, set); !errors.Is(err, inventory.ErrSetExists) {
			t.Fatalf("err = %v, want ErrSetExists", err)
		}
	})
}

// TestEntSetStore_DeleteLeavesTheFleetAlone is the assertion behind the
// "deleting a shared collection must never delete somebody's servers"
// comment. The join rows cascade; the rows they point at do not.
func TestEntSetStore_DeleteLeavesTheFleetAlone(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")
	dev := newDevice(t, client, "rtr-01", org)
	grp := newGroup(t, client, "edge", dev)

	set := mustCreate(t, store, inventory.Set{
		Name: "production", OrganizationID: org, GroupIDs: []int{grp}, DeviceIDs: []int{dev},
	})

	if err := store.Delete(ctx, set.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, set.ID); !errors.Is(err, inventory.ErrSetNotFound) {
		t.Fatalf("set survived its own delete: %v", err)
	}
	if n := client.Device.Query().CountX(ctx); n != 1 {
		t.Errorf("%d devices survive, want 1: deleting an inventory deleted hardware", n)
	}
	if n := client.Group.Query().CountX(ctx); n != 1 {
		t.Errorf("%d groups survive, want 1", n)
	}
}

func TestEntSetStore_DeleteReportsAMissingSet(t *testing.T) {
	store, _ := newTestSetStore(t)

	if err := store.Delete(context.Background(), 4242); !errors.Is(err, inventory.ErrSetNotFound) {
		t.Fatalf("err = %v, want ErrSetNotFound", err)
	}
}

// TestEntSetStore_SetsForDevice covers both routes a device can be reachable
// through. This is what fills ScopeTarget.InventoryIDs, so an omission here
// silently denies a legitimate share and an over-inclusion silently grants
// one, which is why it is one query in one place.
func TestEntSetStore_SetsForDevice(t *testing.T) {
	store, client := newTestSetStore(t)
	ctx := context.Background()
	org := newOrg(t, client, "acme")

	direct := newDevice(t, client, "direct", org)
	grouped := newDevice(t, client, "grouped", org)
	lonely := newDevice(t, client, "lonely", org)
	grp := newGroup(t, client, "edge", grouped)

	byDevice := mustCreate(t, store, inventory.Set{Name: "by-device", OrganizationID: org, DeviceIDs: []int{direct}})
	byGroup := mustCreate(t, store, inventory.Set{Name: "by-group", OrganizationID: org, GroupIDs: []int{grp}})

	t.Run("attached directly", func(t *testing.T) {
		got, err := store.SetsForDevice(ctx, direct)
		if err != nil {
			t.Fatalf("SetsForDevice: %v", err)
		}
		if len(got) != 1 || got[0] != byDevice.ID {
			t.Fatalf("got %v, want [%d]", got, byDevice.ID)
		}
	})

	t.Run("reachable through a group", func(t *testing.T) {
		got, err := store.SetsForDevice(ctx, grouped)
		if err != nil {
			t.Fatalf("SetsForDevice: %v", err)
		}
		if len(got) != 1 || got[0] != byGroup.ID {
			t.Fatalf("got %v, want [%d]", got, byGroup.ID)
		}
	})

	t.Run("in nothing", func(t *testing.T) {
		got, err := store.SetsForDevice(ctx, lonely)
		if err != nil {
			t.Fatalf("SetsForDevice: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})
}

func TestEntSetStore_ListOrganizationsIsSortedByName(t *testing.T) {
	store, client := newTestSetStore(t)

	newOrg(t, client, "zulu")
	newOrg(t, client, "alpha")

	orgs, err := store.ListOrganizations(context.Background())
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("returned %d organizations, want 2", len(orgs))
	}
	// Sorted, because the only consumer is a <select> and a control whose
	// options move between page loads is a control people misclick.
	if orgs[0].Name != "alpha" || orgs[1].Name != "zulu" {
		t.Errorf("order = %q, %q; want alpha then zulu", orgs[0].Name, orgs[1].Name)
	}
}

// TestSet_DeviceCountCountsDirectMembersOnly pins the documented meaning of
// the field, which is easy to misread as "how many devices does this reach".
func TestSet_DeviceCountCountsDirectMembersOnly(t *testing.T) {
	set := inventory.Set{DeviceIDs: []int{1, 2}, GroupIDs: []int{9}}
	if got := set.DeviceCount(); got != 2 {
		t.Errorf("DeviceCount() = %d, want 2 direct members", got)
	}
}

// mustCreate persists a set or fails the test, so the assertions in each
// case are about the behaviour under test rather than about fixture setup.
func mustCreate(t *testing.T, store inventory.SetStore, set inventory.Set) inventory.Set {
	t.Helper()
	created, err := store.Create(context.Background(), set)
	if err != nil {
		t.Fatalf("creating fixture set %q: %v", set.Name, err)
	}
	return created
}

// names projects a slice of sets onto their names, so a failure message
// reads as a list of inventories rather than a wall of structs.
func names(sets []inventory.Set) []string {
	out := make([]string, 0, len(sets))
	for _, s := range sets {
		out = append(out, s.Name)
	}
	return out
}
