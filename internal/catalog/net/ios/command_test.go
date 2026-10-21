// Package ios_test: net.ios.command's registration and declared contract.
package ios_test

import (
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ios"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestCommand_Registered proves "net.ios.command" registered itself as an
// implemented, target-side method with no check: an arbitrary command's
// effect cannot be predicted without running it.
func TestCommand_Registered(t *testing.T) {
	d, ok := collection.Lookup("net.ios.command")
	if !ok {
		t.Fatal("net.ios.command is not registered")
	}
	m := d.Manifest
	if m.Status != collection.StatusImplemented || d.Invoke == nil {
		t.Errorf("status %v, invoke set %v", m.Status, d.Invoke != nil)
	}
	if m.ExecutionContext.Site != collection.SiteTarget || m.ExecutionContext.Device != collection.DeviceRequired {
		t.Errorf("execution context %+v", m.ExecutionContext)
	}
	if m.SupportsCheck || d.Check != nil || m.NoCheckReason == "" {
		t.Errorf("check support %v with check %v and reason %q", m.SupportsCheck, d.Check != nil, m.NoCheckReason)
	}
	if m.Reversibility.Reversible || m.Reversibility.Notes == "" {
		t.Errorf("reversibility %+v", m.Reversibility)
	}
}
