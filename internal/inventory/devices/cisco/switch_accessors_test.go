package cisco_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cisco"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginv "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// newSwitch builds a Switch from the given properties.
func newSwitch(t *testing.T, props map[string]pkginv.PropertyValue) pkginv.InventoryItem {
	t.Helper()

	item, err := cisco.NewSwitch(record.Record{
		ID:         "sw-1",
		Name:       "sw1",
		Type:       "cisco_switch",
		Properties: props,
		State:      pkginv.StateActive,
	})
	if err != nil {
		t.Fatalf("NewSwitch: %v", err)
	}
	return item
}

// TestSwitchAccessors covers the accessors that make Switch structurally
// satisfy its declared capabilities.
//
// The two property-key sources matter here. A switch can arrive either from
// a hand-written hosts.yaml entry or from a Catalyst Center sync, and the
// two spell the firmware version differently, so the accessor has to answer
// for both or a synced switch would report an empty IOS version.
func TestSwitchAccessors(t *testing.T) {
	tests := []struct {
		name        string
		props       map[string]pkginv.PropertyValue
		wantHost    string
		wantPort    int
		wantVersion string
		wantNetconf bool
		wantPrompt  string
	}{
		{
			name: "hand-written inventory entry",
			props: map[string]pkginv.PropertyValue{
				"host":            "10.0.0.1",
				"port":            2222,
				"ios_version":     "17.3.2",
				"netconf_enabled": true,
				"cli_prompt":      "sw1#",
			},
			wantHost:    "10.0.0.1",
			wantPort:    2222,
			wantVersion: "17.3.2",
			wantNetconf: true,
			wantPrompt:  "sw1#",
		},
		{
			name: "synced from a Catalyst Center",
			props: map[string]pkginv.PropertyValue{
				"host":                      "10.10.20.175",
				"catalyst_software_version": "17.12.1prd9",
			},
			wantHost:    "10.10.20.175",
			wantPort:    22,
			wantVersion: "17.12.1prd9",
		},
		{
			name: "the catalyst key wins over the generic one",
			props: map[string]pkginv.PropertyValue{
				"catalyst_software_version": "17.12.1prd9",
				"ios_version":               "stale",
			},
			wantVersion: "17.12.1prd9",
			wantPort:    22,
		},
		{
			name: "an empty catalyst value falls through",
			props: map[string]pkginv.PropertyValue{
				"catalyst_software_version": "",
				"ios_version":               "17.3.2",
			},
			wantVersion: "17.3.2",
			wantPort:    22,
		},
		{
			name:     "nothing set at all",
			props:    nil,
			wantPort: 22,
		},
		{
			name:     "an explicit zero port still defaults",
			props:    map[string]pkginv.PropertyValue{"port": 0},
			wantPort: 22,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := newSwitch(t, tt.props)

			ssh, ok := item.(capability.SSHTransportCapable)
			if !ok {
				t.Fatal("Switch does not structurally implement SSHTransportCapable")
			}
			if got := ssh.SSHHost(); got != tt.wantHost {
				t.Errorf("SSHHost() = %q, want %q", got, tt.wantHost)
			}
			if got := ssh.SSHPort(); got != tt.wantPort {
				t.Errorf("SSHPort() = %d, want %d", got, tt.wantPort)
			}
			addr, ok := item.(capability.NetworkAddressableCapable)
			if !ok {
				t.Fatal("Switch does not structurally implement NetworkAddressableCapable")
			}
			if got := addr.IPAddress(); got != tt.wantHost {
				t.Errorf("IPAddress() = %q, want %q", got, tt.wantHost)
			}

			ios, ok := item.(capability.CiscoIOSCapable)
			if !ok {
				t.Fatal("Switch does not structurally implement CiscoIOSCapable")
			}
			if got := ios.IOSVersion(); got != tt.wantVersion {
				t.Errorf("IOSVersion() = %q, want %q", got, tt.wantVersion)
			}
			if got := ios.SupportsNETCONF(); got != tt.wantNetconf {
				t.Errorf("SupportsNETCONF() = %v, want %v", got, tt.wantNetconf)
			}

			cli, ok := item.(capability.NetworkCLICapable)
			if !ok {
				t.Fatal("Switch does not structurally implement NetworkCLICapable")
			}
			if got := cli.CLIPrompt(); got != tt.wantPrompt {
				t.Errorf("CLIPrompt() = %q, want %q", got, tt.wantPrompt)
			}
		})
	}
}

// TestSwitchHasCapability proves the declared-and-structural conjunction
// holds for every capability this type claims, which is what makes a true
// HasCapability result a guarantee rather than a hope.
func TestSwitchHasCapability(t *testing.T) {
	item := newSwitch(t, map[string]pkginv.PropertyValue{"host": "10.0.0.1"})

	for _, name := range []capability.Name{
		capability.NameSSHTransport,
		capability.NameCiscoIOS,
		capability.NameNetworkCLI,
	} {
		if !item.HasCapability(name) {
			t.Errorf("switch does not report %s, capabilities: %v", name, item.Capabilities())
		}
	}

	// A switch is not a Catalyst Center controller, and claiming the
	// controller's API capability would make a net.catalyst.* method try to
	// address a switch as though it were the controller managing it.
	if item.HasCapability(capability.NameCatalystAPI) {
		t.Error("switch reports CatalystAPICapable, which belongs to the controller, not the devices it manages")
	}
}

// TestSwitchUnionsClassificationCapabilities proves a classification-derived
// capability is added to the vendor baseline rather than replacing it, per
// the Phase 32 granularity decision.
func TestSwitchUnionsClassificationCapabilities(t *testing.T) {
	item, err := cisco.NewSwitch(record.Record{
		ID:           "sw-2",
		Name:         "sw2",
		Type:         "cisco_switch",
		State:        pkginv.StateActive,
		Capabilities: []capability.Name{capability.NameNetconf},
	})
	if err != nil {
		t.Fatalf("NewSwitch: %v", err)
	}

	var found bool
	for _, c := range item.Capabilities() {
		if c == capability.NameNetconf {
			found = true
			break
		}
	}
	if !found {
		t.Error("a classification-derived capability was not unioned into the declared set")
	}
	// And the vendor baseline survived alongside it.
	if !item.HasCapability(capability.NameCiscoIOS) {
		t.Error("the vendor baseline was replaced rather than added to")
	}
}
