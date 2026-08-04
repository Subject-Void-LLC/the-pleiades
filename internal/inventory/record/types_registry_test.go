package record_test

import (
	"testing"

	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/cisco"
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
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
