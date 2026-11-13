// Tests for CheckCapabilities.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestCheckCapabilities covers every answer CheckCapabilities gives,
// including a child capability satisfying its parent.
func TestCheckCapabilities(t *testing.T) {
	needsSystemd := collection.Manifest{RequiredCapabilities: []capability.Name{capability.NameSystemd}}
	needsServiceManager := collection.Manifest{RequiredCapabilities: []capability.Name{capability.NameServiceManager}}
	stub := func(caps ...capability.Name) *inventorytest.Stub {
		return &inventorytest.Stub{StubName: "d1", Caps: caps}
	}

	tests := []struct {
		name     string
		device   *inventorytest.Stub
		manifest collection.Manifest
		wantErr  string
	}{
		{"a method requiring nothing has nothing to check", stub(), collection.Manifest{}, ""},
		{"a device with the capability passes", stub(capability.NameSystemd), needsSystemd, ""},
		{"a child capability satisfies its parent", stub(capability.NameSystemd), needsServiceManager, ""},
		{"a device without it is refused, by name", stub(capability.NameCiscoIOS), needsSystemd,
			`collection method "x.y" requires capability SystemdCapable, which device "d1" does not have`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := collection.CheckCapabilities(tt.device, "x.y", tt.manifest)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// notingStub is a device that records grants, saying what became of each
// (inventory.GrantNoter), as every device built on record.Base does.
type notingStub struct {
	*inventorytest.Stub
	notes []inventory.GrantNote
}

// GrantNotes implements inventory.GrantNoter.
func (s notingStub) GrantNotes() []inventory.GrantNote { return s.notes }

// TestCheckCapabilities_ALapsedGrantSaysWhy: a device without a capability
// it records a lapsed grant of is refused with the grant's own reason; a
// warning, or a lapse of another capability, adds nothing.
func TestCheckCapabilities_ALapsedGrantSaysWhy(t *testing.T) {
	m := collection.Manifest{RequiredCapabilities: []capability.Name{"acme.widget_api"}}
	why := "program \"widgets\"'s definition of acme.widget_api (revision 2) is not the one device d1's grant was made against; onboard it again: `pleiades onboard d1`"
	for _, tc := range []struct {
		name  string
		notes []inventory.GrantNote
		want  string
	}{
		{"a lapse of the capability", []inventory.GrantNote{{Capability: "acme.widget_api", Lapsed: true, Reason: why}}, `which device "d1" does not have: ` + why},
		{"a warning only", []inventory.GrantNote{{Capability: "acme.widget_api", Reason: "a warning"}}, `which device "d1" does not have`},
		{"a lapse of another", []inventory.GrantNote{{Capability: "acme.other", Lapsed: true, Reason: "elsewhere"}}, `which device "d1" does not have`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := notingStub{Stub: &inventorytest.Stub{StubName: "d1"}, notes: tc.notes}
			err := collection.CheckCapabilities(device, "acme.widget.read", m)
			if err == nil || !strings.HasSuffix(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to end %q", err, tc.want)
			}
		})
	}
}

// TestCheckCapabilities_ANilDeviceIsRefusedOnlyWhenOneIsRequired: a call
// with no device runs a method that requires nothing, and is refused for
// one that requires a capability, since nothing can supply it.
func TestCheckCapabilities_ANilDeviceIsRefusedOnlyWhenOneIsRequired(t *testing.T) {
	if err := collection.CheckCapabilities(nil, "x.y", collection.Manifest{}); err != nil {
		t.Errorf("err = %v, want none for a method requiring nothing", err)
	}
	m := collection.Manifest{RequiredCapabilities: []capability.Name{capability.NameSystemd}}
	if err := collection.CheckCapabilities(nil, "x.y", m); err == nil || !strings.Contains(err.Error(), "no target device") {
		t.Errorf("err = %v, want a refusal for a call with no device", err)
	}
}
