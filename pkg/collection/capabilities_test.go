// Tests for CheckCapabilities.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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
