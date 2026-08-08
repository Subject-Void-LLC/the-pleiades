package catalyst_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/catalyst"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginv "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// newCenter builds a Center from the given properties.
func newCenter(t *testing.T, props map[string]pkginv.PropertyValue) pkginv.InventoryItem {
	t.Helper()

	item, err := catalyst.NewCenter(record.Record{
		ID:         "c-1",
		Name:       "controller",
		Type:       "catalyst_center",
		Properties: props,
		State:      pkginv.StateActive,
	})
	if err != nil {
		t.Fatalf("NewCenter: %v", err)
	}
	return item
}

// TestCatalystBaseURL covers the accessor that satisfies
// CatalystAPICapable, including the fallback that keeps a hand-added
// controller addressable.
//
// The fallback is the case worth pinning. `pleiades add-host` writes host,
// not catalyst_base_url, so without it a controller onboarded by hand would
// declare a capability it could not honor, and every net.catalyst.* method
// targeting it would fail on an empty URL rather than on anything
// diagnosable.
func TestCatalystBaseURL(t *testing.T) {
	tests := []struct {
		name  string
		props map[string]pkginv.PropertyValue
		want  string
	}{
		{
			name:  "explicit base URL from a sync",
			props: map[string]pkginv.PropertyValue{"catalyst_base_url": "https://dnac.example.test"},
			want:  "https://dnac.example.test",
		},
		{
			name:  "falls back to a hand-added host",
			props: map[string]pkginv.PropertyValue{"host": "dnac.example.test"},
			want:  "https://dnac.example.test",
		},
		{
			name: "explicit base URL wins over host",
			props: map[string]pkginv.PropertyValue{
				"catalyst_base_url": "https://real.example.test",
				"host":              "stale.example.test",
			},
			want: "https://real.example.test",
		},
		{
			name:  "empty base URL when neither is set",
			props: nil,
			want:  "",
		},
		{
			name:  "an empty stored value falls through to host",
			props: map[string]pkginv.PropertyValue{"catalyst_base_url": "", "host": "dnac.example.test"},
			want:  "https://dnac.example.test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := newCenter(t, tt.props)

			addressable, ok := item.(capability.CatalystAPICapable)
			if !ok {
				t.Fatal("Center does not structurally implement CatalystAPICapable")
			}
			if got := addressable.CatalystBaseURL(); got != tt.want {
				t.Errorf("CatalystBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestHasCapability proves the declared-and-structural conjunction holds:
// the controller advertises the API capability it implements and does not
// advertise a transport it has no way to speak.
func TestHasCapability(t *testing.T) {
	item := newCenter(t, map[string]pkginv.PropertyValue{"catalyst_base_url": "https://dnac.example.test"})

	if !item.HasCapability(capability.NameCatalystAPI) {
		t.Errorf("controller does not report %s, capabilities: %v", capability.NameCatalystAPI, item.Capabilities())
	}
	// A controller answers a REST API; nothing reaches it over a terminal
	// session, so claiming either of these would be a false promise a
	// dispatcher would then act on.
	for _, name := range []capability.Name{capability.NameSSHTransport, capability.NameNetworkCLI} {
		if item.HasCapability(name) {
			t.Errorf("controller reports %s, which it has no way to satisfy", name)
		}
	}
}
