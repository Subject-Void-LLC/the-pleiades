// Tests of IsShared, which decides whether a device's tasks take a shared
// lock.
package inventory_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// sharedItem is a device saying whether it is shared.
type sharedItem struct {
	inventory.InventoryItem
	shared bool
}

func (s sharedItem) SharedAcrossRuns() bool { return s.shared }

// plainItem is a device that says nothing about sharing.
type plainItem struct{ inventory.InventoryItem }

// TestIsShared: only a device that implements Shared and says so is
// shared; one that says not, and one that says nothing, are exclusive.
func TestIsShared(t *testing.T) {
	for name, tc := range map[string]struct {
		item inventory.InventoryItem
		want bool
	}{
		"says shared":     {sharedItem{shared: true}, true},
		"says not shared": {sharedItem{shared: false}, false},
		"says nothing":    {plainItem{}, false},
	} {
		if got := inventory.IsShared(tc.item); got != tc.want {
			t.Errorf("%s: IsShared = %v, want %v", name, got, tc.want)
		}
	}
}
