// Package ios_test: net.ios.parse's registration as an implemented,
// read-only, checkable method.
package ios_test

import (
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ios"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestParse_Registered proves "net.ios.parse" registered itself through
// this package's init() as an implemented, read-only method whose check
// is its own run.
func TestParse_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ios.parse")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "net.ios.parse")
	}
	if d.Manifest.Status != collection.StatusImplemented || d.Invoke == nil {
		t.Errorf("Manifest.Status = %v with Invoke set = %v, want implemented", d.Manifest.Status, d.Invoke != nil)
	}
	if !d.Manifest.Reversibility.ReadOnly || d.Manifest.Reversibility.Reversible {
		t.Errorf("Manifest.Reversibility = %+v, want read-only", d.Manifest.Reversibility)
	}
	if !d.Manifest.SupportsCheck || d.Check == nil {
		t.Errorf("SupportsCheck = %v with Check set = %v, want a check that runs it", d.Manifest.SupportsCheck, d.Check != nil)
	}
	if d.Manifest.ExecutionContext.Site != collection.SiteTarget || d.Manifest.ExecutionContext.Device != collection.DeviceRequired {
		t.Errorf("Manifest.ExecutionContext = %+v", d.Manifest.ExecutionContext)
	}
}
