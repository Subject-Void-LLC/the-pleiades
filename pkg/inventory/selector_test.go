package inventory_test

import (
	"testing"

	inventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestMembership_EmptyIsNotTheSameAsAbsent covers the distinction the
// pointer on Selector.Membership exists to preserve.
//
// Every other Selector field means "no restriction" when it is empty, which
// is right for a filter and lethal for a boundary: an inventory that
// contains nothing would then select the entire fleet, so dispatching an
// empty inventory would reach every device the platform manages. A nil
// Membership means no membership restriction; a non-nil empty one restricts
// to nothing.
func TestMembership_EmptyIsNotTheSameAsAbsent(t *testing.T) {
	var absent inventory.Selector
	if absent.Membership != nil {
		t.Fatal("the zero Selector carries a membership, so it would restrict rather than stream everything")
	}

	empty := inventory.Selector{Membership: &inventory.Membership{}}
	if !empty.Membership.Empty() {
		t.Error("a membership holding no groups and no devices does not report itself empty")
	}

	for name, m := range map[string]inventory.Membership{
		"groups only":  {GroupIDs: []int{1}},
		"devices only": {DeviceIDs: []int{1}},
		"both":         {GroupIDs: []int{1}, DeviceIDs: []int{2}},
	} {
		if m.Empty() {
			t.Errorf("a membership with %s reports itself empty, which would select nothing", name)
		}
	}
}
