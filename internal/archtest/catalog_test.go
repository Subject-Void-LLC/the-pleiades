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
// entry is actually registered in pkg/collection, catching a stale
// internal/catalog/builtins.go (missing a blank import) or a catalogdata
// entry never regenerated at all.
//
// It used to also assert every entry was declared, on the grounds that
// Phase 34 generates stubs and never implementations. That was true when
// nothing was implemented and stopped being true the moment something was:
// the net.catalyst.* methods are verified against a real Cisco Catalyst
// Center and carry status implemented. The invariant worth keeping is not
// "everything is a stub" but the one below, which holds at both ends of
// that transition and is what actually prevents a lie: status and the
// presence of an implementation must agree.
func TestCatalogCollections_AllRegistered(t *testing.T) {
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			t.Errorf("catalogdata.Collections entry %q is not registered in pkg/collection; internal/catalog/builtins.go may be stale (run `go generate ./internal/forge/catalogdata`)", cfg.Name)
			continue
		}

		switch desc.Manifest.Status {
		case collection.StatusDeclared:
			if desc.Invoke != nil {
				t.Errorf("%q is declared but carries an implementation; flip its status to %v or remove the Invoke",
					cfg.Name, collection.StatusImplemented)
			}
		case collection.StatusImplemented:
			if desc.Invoke == nil {
				t.Errorf("%q claims status %v but carries no implementation, so a runbook calling it would fail at run time",
					cfg.Name, collection.StatusImplemented)
			}
		default:
			t.Errorf("%q has unknown Manifest.Status %q", cfg.Name, desc.Manifest.Status)
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
