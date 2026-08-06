// This file proves the generated module catalog (Phase 34) stays in sync
// with its own source of truth, internal/forge/catalogdata: every entry
// there must be reachable through the real registries the generated files
// and their builtins aggregators feed. Without this, a catalogdata edit
// that was never regenerated, or a regeneration that silently dropped an
// entry from internal/catalog/builtins.go, would be a silent drift no
// test catches until a much later phase leans on the missing name.
package archtest

import (
	"testing"

	_ "github.com/SubjectVoidLLC/the-pleiades/internal/catalog"   // triggers every generated Collection package's init()
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory" // triggers builtins.go's device-type init()s

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/catalogdata"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

// TestCatalogCollections_AllRegistered proves every catalogdata.Collections
// entry is actually registered in pkg/collection with the expected
// declared-not-implemented status, catching a stale
// internal/catalog/builtins.go (missing a blank import) or a catalogdata
// entry never regenerated at all.
func TestCatalogCollections_AllRegistered(t *testing.T) {
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			t.Errorf("catalogdata.Collections entry %q is not registered in pkg/collection; internal/catalog/builtins.go may be stale (run `go generate ./internal/forge/catalogdata`)", cfg.Name)
			continue
		}
		if desc.Manifest.Status != collection.StatusDeclared {
			t.Errorf("%q has Manifest.Status %v, want %v: Phase 34 generates every catalog entry as declared, never implemented", cfg.Name, desc.Manifest.Status, collection.StatusDeclared)
		}
	}
}

// TestCatalogDevices_AllRegistered proves every catalogdata.Devices entry
// is reachable through record.LookupType, catching a missing blank import
// in internal/inventory/builtins.go.
func TestCatalogDevices_AllRegistered(t *testing.T) {
	for _, cfg := range catalogdata.Devices {
		if _, ok := record.LookupType(cfg.TypeKey); !ok {
			t.Errorf("catalogdata.Devices entry %q is not registered in record.Types; internal/inventory/builtins.go may be missing its blank import", cfg.TypeKey)
		}
	}
}
