package record

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// Constructor is the shape every device type registers under: a function
// hydrating a Record into a concrete InventoryItem. internal/inventory's
// ItemFactory.Build is the one caller that resolves a Record's Type
// against this shape.
type Constructor func(Record) (inventory.InventoryItem, error)

// typesRegistry is the Section 25 "typed generic Registry" for device
// types, built on pkg/registry.Registry so this is the one Registry
// implementation the codebase has, not a second hand-rolled map sitting
// next to it. It lives here, in the leaf package both internal/inventory
// (factory.go) and the vendor device packages (devices/cisco,
// devices/linux) already import, rather than in internal/inventory
// itself: putting it there would force a vendor package to import
// internal/inventory to call Register, while internal/inventory
// blank-imports the vendor package to trigger that same call's init(),
// which is exactly the import cycle this package's own doc comment
// explains it exists to avoid.
var typesRegistry = registry.New[Constructor]()

// SnapshotForTest captures the process-wide device type registry and returns a
// function that puts it back, for a test that registers into it.
//
// Without this a test's registration outlives the test, so a second
// iteration under `go test -count>1` fails on a duplicate registration
// rather than starting clean. Call it once at the top of such a test:
//
//	t.Cleanup(record.SnapshotForTest())
//
// It is exported rather than living in an export_test.go because a
// _test.go file cannot be imported across package boundaries, and tests in
// other packages register here too. internal/archtest forbids production
// code from calling it.
func SnapshotForTest() func() {
	restoreTypes := typesRegistry.SnapshotForTest()
	restoreOnboarded := onboardedTypes.SnapshotForTest()
	restoreDispatch := dispatchProperties.SnapshotForTest()
	return func() {
		restoreTypes()
		restoreOnboarded()
		restoreDispatch()
	}
}

// RegisterType adds deviceType's constructor to the shared registry. Each
// vendor device package calls this from its own init() (see
// devices/cisco/router.go, devices/linux/server.go) instead of
// internal/inventory/factory.go's NewItemFactory hardcoding the call
// itself; adding a new in-tree device type is then writing the package
// plus one blank import to trigger its init (internal/inventory/
// builtins.go), never touching factory.go's own logic. It panics on a
// duplicate deviceType key, matching pkg/capability's identical
// closed-vocabulary-at-init-time convention: two device type packages
// claiming the same type string is a build-time programming error, not a
// runtime condition to recover from.
func RegisterType(deviceType string, constructor Constructor) {
	typesRegistry.MustRegister(deviceType, constructor)
}

// LookupType returns the constructor registered for deviceType, if any.
func LookupType(deviceType string) (Constructor, bool) {
	return typesRegistry.Get(deviceType)
}

// AllTypes returns a snapshot of every currently registered device type,
// keyed by type string. internal/inventory.NewItemFactory builds its
// batteries-included factory from this rather than importing each vendor
// package by name.
func AllTypes() map[string]Constructor {
	return typesRegistry.All()
}
