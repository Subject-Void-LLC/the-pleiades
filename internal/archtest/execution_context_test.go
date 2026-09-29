// This file holds every built-in Collection method to PLAN.md Section 14's
// execution context (Phase 117a): each states where its code runs and
// whether it acts on a device, and the catalog's own source,
// internal/forge/catalogdata, agrees with what the method registered, so a
// method scaffolded from that source is born saying the same thing.
package archtest

import (
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog" // triggers every Collection package's init()

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestEveryBuiltinStatesItsExecutionContext: an empty Site or Device reads
// as target-side with a device required, which is the right default for an
// external program that predates the fields and the wrong thing to leave
// implicit for a built-in, where it decides whether a task inherits its
// runbook's hosts:. So every built-in says both, declared methods included.
func TestEveryBuiltinStatesItsExecutionContext(t *testing.T) {
	var checked int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok || desc.Provider != nil {
			continue
		}
		checked++
		ec := desc.Manifest.ExecutionContext
		if ec.Site == "" {
			t.Errorf("%s does not state its execution site (Manifest.ExecutionContext.Site)", cfg.Name)
		}
		if ec.Device == "" {
			t.Errorf("%s does not state whether it acts on a device (Manifest.ExecutionContext.Device)", cfg.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no catalogdata entry resolved in the registry, so this test proved nothing")
	}
}

// TestCatalogDataExecutionContextMatchesTheRegistry: catalogdata's Site and
// Device, empty meaning target-side with a device required as the scaffold
// renders them, are what each method registered. A drift here is a method
// whose next scaffold would say something different from its code.
func TestCatalogDataExecutionContextMatchesTheRegistry(t *testing.T) {
	var controllerSide int
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		wantSite, wantDevice := cfg.Site, cfg.Device
		if wantSite == "" {
			wantSite = collection.SiteTarget
		}
		if wantDevice == "" {
			wantDevice = collection.DeviceRequired
		}
		ec := desc.Manifest.ExecutionContext
		if ec.Site != wantSite || ec.Device != wantDevice {
			t.Errorf("%s registers site %q and device %q, and catalogdata says %q and %q",
				cfg.Name, ec.Site, ec.Device, wantSite, wantDevice)
		}
		if ec.Site == collection.SiteController {
			controllerSide++
		}
	}
	if controllerSide == 0 {
		t.Fatal("no controller-side method was found, so the comparison never left the default")
	}
}
