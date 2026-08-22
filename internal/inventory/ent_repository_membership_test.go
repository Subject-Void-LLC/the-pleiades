package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// This file covers Selector.Membership: the contents of one Inventory, as a
// device stream.
//
// Every assertion here is against a real database, because what is being
// tested is a pushed-down SQL predicate. The one that matters most is the
// empty case, which is a claim about what ent renders for an empty IN
// clause and could not be checked any other way.

// membershipFixture is two groups, a directly attached device, a device
// reachable through both a group and directly, and a device in neither.
type membershipFixture struct {
	repo    inventory.Repository
	client  *ent.Client
	groupA  int
	groupB  int
	direct  int
	bothWay int
}

func newMembershipFixture(t *testing.T) membershipFixture {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	mkDevice := func(name string) *ent.Device {
		return client.Device.Create().
			SetName(name).
			SetType("linux_server").
			SetProperties(map[string]interface{}{"host": "10.0.0.1"}).
			SaveX(ctx)
	}

	inA := mkDevice("in-group-a")
	inB := mkDevice("in-group-b")
	direct := mkDevice("attached-directly")
	both := mkDevice("in-a-group-and-attached")
	mkDevice("in-nothing")

	groupA := client.Group.Create().SetName("group-a").AddDevices(inA, both).SaveX(ctx)
	groupB := client.Group.Create().SetName("group-b").AddDevices(inB).SaveX(ctx)

	return membershipFixture{
		repo:    inventory.NewEntRepository(client, inventory.NewItemFactory()),
		client:  client,
		groupA:  groupA.ID,
		groupB:  groupB.ID,
		direct:  direct.ID,
		bothWay: both.ID,
	}
}

// drainSorted iterates to completion and returns the names, sorted, so an
// assertion is about the set rather than about batch ordering.
func drainSorted(t *testing.T, ctx context.Context, it inventory.Iterator) []string {
	names := drainNames(t, ctx, it)
	sort.Strings(names)
	return names
}

func TestEntRepository_MembershipStreamsGroupsAndDirectDevicesTogether(t *testing.T) {
	f := newMembershipFixture(t)
	ctx := context.Background()

	it, err := f.repo.GetGroup(ctx, pkginventory.Selector{Membership: &pkginventory.Membership{
		GroupIDs:  []int{f.groupA, f.groupB},
		DeviceIDs: []int{f.direct},
	}})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}

	got := drainSorted(t, ctx, it)
	want := []string{"attached-directly", "in-a-group-and-attached", "in-group-a", "in-group-b"}
	if len(got) != len(want) {
		t.Fatalf("membership streamed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("membership streamed %v, want %v", got, want)
		}
	}
}

func TestEntRepository_MembershipYieldsADoublyReachableDeviceOnce(t *testing.T) {
	f := newMembershipFixture(t)
	ctx := context.Background()

	// One device is both in group A and attached directly. A caller that
	// ran two queries and concatenated them would dispatch to it twice,
	// which on a real change means running the same command on the same
	// box twice. The OR is pushed down so the database de-duplicates.
	it, err := f.repo.GetGroup(ctx, pkginventory.Selector{Membership: &pkginventory.Membership{
		GroupIDs:  []int{f.groupA},
		DeviceIDs: []int{f.bothWay},
	}})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}

	counts := map[string]int{}
	for _, name := range drainNames(t, ctx, it) {
		counts[name]++
	}
	if counts["in-a-group-and-attached"] != 1 {
		t.Errorf("a device reachable both ways was streamed %d times, want 1",
			counts["in-a-group-and-attached"])
	}
	if len(counts) != 2 {
		t.Errorf("membership streamed %d distinct devices, want 2: %v", len(counts), counts)
	}
}

// TestEntRepository_AnEmptyMembershipSelectsNothing is the assertion this
// whole field shape exists for.
//
// Every other Selector field means "no restriction" when it is empty, which
// is right for a filter and catastrophic for a boundary: an inventory that
// contains nothing would select the entire fleet, so dispatching an empty
// inventory would reach every device the platform manages. The pointer is
// what distinguishes "no membership restriction" from "a membership that is
// empty", and this proves the distinction survives all the way into SQL,
// where it depends on ent rendering an empty IN as FALSE.
func TestEntRepository_AnEmptyMembershipSelectsNothing(t *testing.T) {
	f := newMembershipFixture(t)
	ctx := context.Background()

	// First, the control: no membership at all really does stream
	// everything, so the assertion below is about the membership and not
	// about an empty database.
	all, err := f.repo.GetGroup(ctx, pkginventory.Selector{})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if names := drainNames(t, ctx, all); len(names) != 5 {
		t.Fatalf("the fixture holds %d devices, want 5: this test cannot detect over-selection otherwise", len(names))
	}

	it, err := f.repo.GetGroup(ctx, pkginventory.Selector{Membership: &pkginventory.Membership{}})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if names := drainNames(t, ctx, it); len(names) != 0 {
		t.Errorf("an empty membership streamed %d devices (%v), want none: an empty inventory must not select the fleet",
			len(names), names)
	}
}

func TestSet_SelectorIsTheOneDefinitionOfMembership(t *testing.T) {
	set := inventory.Set{GroupIDs: []int{4, 9}, DeviceIDs: []int{7}}

	sel := set.Selector()
	if sel.Membership == nil {
		t.Fatal("Set.Selector() returned no membership, so it would select every device")
	}
	if len(sel.Membership.GroupIDs) != 2 || len(sel.Membership.DeviceIDs) != 1 {
		t.Errorf("Set.Selector() = %+v, want both halves of the membership", sel.Membership)
	}
	if set.Empty() {
		t.Error("a Set holding groups and devices reports itself empty")
	}

	// An empty Set still produces a membership, rather than a zero
	// Selector. This is the line between "this inventory contains nothing"
	// and "no restriction", and they are one keystroke apart.
	empty := inventory.Set{}
	if empty.Selector().Membership == nil {
		t.Error("an empty Set produced an unrestricted selector, which would dispatch to every device")
	}
	if !empty.Empty() {
		t.Error("an empty Set does not report itself empty")
	}
}

// TestFileRepository_RefusesAMembershipItCannotResolve is the other half of
// the boundary.
//
// The Crawl tier has no groups and no inventories, so it cannot narrow to a
// membership at all. It refuses rather than ignoring, unlike its treatment
// of Selector.GroupName: an ignored filter fails to narrow, but an ignored
// membership streams the whole file, and on a dispatch that is the
// difference between a filter and a safety boundary.
func TestFileRepository_RefusesAMembershipItCannotResolve(t *testing.T) {
	hostsPath := filepath.Join(t.TempDir(), "hosts.yaml")
	if err := inventory.WriteHosts(hostsPath, []inventory.HostSpec{
		{Name: "walk-1", Type: "linux_server"},
	}); err != nil {
		t.Fatalf("seeding hosts: %v", err)
	}

	repo := inventory.NewFileRepository(hostsPath, inventory.NewItemFactory())

	if _, err := repo.GetGroup(context.Background(), pkginventory.Selector{
		Membership: &pkginventory.Membership{GroupIDs: []int{1}},
	}); !errors.Is(err, inventory.ErrSelectorUnsupported) {
		t.Errorf("GetGroup with a membership returned %v, want ErrSelectorUnsupported", err)
	}

	// And it still streams normally without one, so the refusal is about
	// the membership rather than about this backend being broken.
	it, err := repo.GetGroup(context.Background(), pkginventory.Selector{})
	if err != nil {
		t.Fatalf("GetGroup with no membership: %v", err)
	}
	if names := drainNames(t, context.Background(), it); len(names) != 1 {
		t.Errorf("the file repository streamed %d hosts, want 1", len(names))
	}
}
