// Tests for an onboarding whose final save fails: it reports nothing it did
// not store.
package onboard

import (
	"context"
	"errors"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// saveFailing is an inventory whose failOn'th save fails, as a full disk or
// a concurrent edit makes one fail.
type saveFailing struct {
	inv.Repository
	failOn, saves int
}

// Save implements inv.Repository.
func (r *saveFailing) Save(ctx context.Context, item inventory.InventoryItem) error {
	if r.saves++; r.saves == r.failOn {
		return errors.New("the store is full")
	}
	return r.Repository.Save(ctx, item)
}

// TestOnboard_AFailedSaveReportsNoFirstUsePin: a first-use onboarding whose
// final save fails reports no pin, no warning that one was recorded, and no
// change, and stores no pin.
func TestOnboard_AFailedSaveReportsNoFirstUsePin(t *testing.T) {
	cert, _ := deviceCert(t, farOff())
	srv := newSwappable(t, cert)
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic})
	// A generic device starts discovered: the first save moves it to
	// onboarding, the second, with the pin and the discovery, fails, and the
	// third returns it to discovered with neither.
	failing := &saveFailing{Repository: repo, failOn: 2}
	res, err := onboardWith(context.Background(), failing, "api1", staticSecrets(apiLogin), fixedNow, Lookup, options{firstUse: true})
	if err == nil || !strings.Contains(err.Error(), "the store is full") || failing.saves != 3 {
		t.Fatalf("err %v after %d saves, want the second save's failure and a third returning it to discovered", err, failing.saves)
	}
	if item, _ := repo.GetByName(context.Background(), "api1"); item.State() != inventory.StateDiscovered || res.State != "discovered" {
		t.Errorf("after a failed save the device is %s (result %s), want discovered", item.State(), res.State)
	}
	if res.FirstUse != nil || hasWarning(res.Warnings, "recorded the certificate it presented") || res.Changed {
		t.Errorf("a failed save reported first use %+v, warnings %q, changed %v", res.FirstUse, res.Warnings, res.Changed)
	}
	if got := stored(t, repo, "api1", inventory.TLSFirstUseProperty); got != "" {
		t.Errorf("a failed save stored the pin %q", got)
	}
}
