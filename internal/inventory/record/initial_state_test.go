// Tests for the table of device types that start discovered.
package record_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestInitialState: a registered onboarded type starts discovered, any
// other active; a duplicate registration panics; and the snapshot takes
// the registration back.
func TestInitialState(t *testing.T) {
	restore := record.SnapshotForTest()
	record.RegisterOnboardedType("initialstate_test_type")
	if !record.IsOnboardedType("initialstate_test_type") || record.InitialState("initialstate_test_type") != inventory.StateDiscovered {
		t.Fatal("a registered onboarded type does not start discovered")
	}
	if record.InitialState("some_vendor_type") != inventory.StateActive {
		t.Fatal("an unregistered type does not start active")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("a duplicate registration did not panic")
			}
		}()
		record.RegisterOnboardedType("initialstate_test_type")
	}()
	restore()
	if record.IsOnboardedType("initialstate_test_type") {
		t.Error("the snapshot did not take the registration back")
	}
}
