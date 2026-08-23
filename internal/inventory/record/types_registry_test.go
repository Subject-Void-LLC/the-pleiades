package record_test

import (
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cisco"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestBuiltinTypesSelfRegister proves the real cisco/linux init() calls ran
// and registered under the exact keys internal/inventory.NewItemFactory has
// always exposed, so the registry-fed factory is behaviorally identical to
// the previous hardcoded one.
func TestBuiltinTypesSelfRegister(t *testing.T) {
	for _, deviceType := range []string{"cisco_router", "linux_server"} {
		if _, ok := record.LookupType(deviceType); !ok {
			t.Errorf("%q is not registered; expected the vendor package's own init() to have registered it", deviceType)
		}
	}

	all := record.AllTypes()
	if len(all) < 2 {
		t.Fatalf("AllTypes() = %d entries; want at least the 2 built-in types", len(all))
	}
}

func TestLookupType_Miss(t *testing.T) {
	if _, ok := record.LookupType("does_not_exist"); ok {
		t.Error("LookupType on an unregistered type returned ok=true")
	}
}

func TestAllTypes_SnapshotIsIndependent(t *testing.T) {
	all := record.AllTypes()
	before := len(all)

	// Mutating the returned snapshot must never reach the live registry,
	// the same guarantee pkg/registry.Registry.All itself already proves;
	// this test proves record.AllTypes did not accidentally bypass that by
	// returning the live map directly.
	for k := range all {
		delete(all, k)
	}

	after := record.AllTypes()
	if len(after) != before {
		t.Fatalf("AllTypes() after mutating a prior snapshot = %d entries; want unchanged %d", len(after), before)
	}
}

func TestRegisterType_DuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RegisterType on an already-registered type did not panic")
		}
	}()
	record.RegisterType("cisco_router", func(record.Record) (inventory.InventoryItem, error) {
		return nil, nil
	})
}

// TestSnapshotForTest_FreesTheNameForTheNextIteration pins the seam every
// out-of-package test registering a device type depends on.
//
// RegisterType panics on a duplicate, matching pkg/capability's
// closed-vocabulary-at-init convention, so a test that registers a type
// and leaves it there does not merely fail on a second iteration in the
// same process -- it takes the whole test binary down with it. Looping
// here rather than asserting once is the point: one pass proves nothing,
// because the defect this guards against only appears on the second.
func TestSnapshotForTest_FreesTheNameForTheNextIteration(t *testing.T) {
	const deviceType = "record_test_snapshot_roundtrip"

	for i := range 3 {
		func() {
			defer record.SnapshotForTest()()

			record.RegisterType(deviceType, func(record.Record) (inventory.InventoryItem, error) {
				return nil, nil
			})
			if _, ok := record.LookupType(deviceType); !ok {
				t.Fatalf("iteration %d: LookupType(%q) found nothing straight after registering it", i, deviceType)
			}
		}()

		if _, ok := record.LookupType(deviceType); ok {
			t.Fatalf("iteration %d: %q outlived the restore, so the next iteration would panic on it", i, deviceType)
		}
	}
}
