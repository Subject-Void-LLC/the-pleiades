// Tests for the generic device types: that only a discovery grants a
// capability beyond the baseline, that every discoverable capability is
// really implemented, and that each type refuses an unusable record.
package generic_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// addressed is the least each type needs to build.
var addressed = map[string]map[string]inventory.PropertyValue{
	generic.TypeSSH:     {"host": "10.0.0.1"},
	generic.TypeNetconf: {"host": "10.0.0.2"},
	generic.TypeHTTP:    {generic.BaseURLProperty: "https://api.example.com"},
	generic.TypeGRPC:    {generic.GRPCTargetProperty: "grpc.example.com:443"},
}

// buildWith builds deviceType from its address, the extra properties and
// capabilities as classification data.
func buildWith(t *testing.T, deviceType string, extra map[string]inventory.PropertyValue, classified []capability.Name) (inventory.InventoryItem, error) {
	t.Helper()
	props := map[string]inventory.PropertyValue{}
	for k, v := range addressed[deviceType] {
		props[k] = v
	}
	for k, v := range extra {
		props[k] = v
	}
	ctor, ok := record.LookupType(deviceType)
	if !ok {
		t.Fatalf("%s is not registered", deviceType)
	}
	return ctor(record.Record{ID: "d1", Name: "d1", Type: deviceType, Properties: props, Capabilities: classified})
}

func discovery(caps ...capability.Name) inventory.PropertyValue {
	return inventory.Discovery{Protocol: "test", Capabilities: caps}.Property()
}

// boundDiscovery is a discovery of deviceType made against its address in
// addressed, bound as onboarding binds one.
func boundDiscovery(deviceType string, caps ...capability.Name) inventory.PropertyValue {
	d := inventory.Discovery{Protocol: "test", Capabilities: caps}
	d.Binding = generic.Binding(deviceType, inventory.NewProperties(addressed[deviceType]))
	return d.Property()
}

// TestGeneric_OnlyADiscoveryGrants: before onboarding, no discoverable
// capability is held, not even when classification claims all of them;
// after a discovery naming each, every one is held, which proves the type
// implements each one's accessors.
func TestGeneric_OnlyADiscoveryGrants(t *testing.T) {
	for _, deviceType := range generic.Types() {
		t.Run(deviceType, func(t *testing.T) {
			grants := generic.Discoverable(deviceType)
			claimed, err := buildWith(t, deviceType, nil, grants)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range grants {
				if claimed.HasCapability(c) {
					t.Errorf("classification alone granted %s", c)
				}
			}
			onboarded, err := buildWith(t, deviceType, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: boundDiscovery(deviceType, grants...)}, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range grants {
				if !onboarded.HasCapability(c) {
					t.Errorf("a discovery naming %s did not grant it", c)
				}
			}
			if !record.IsOnboardedType(deviceType) || record.InitialState(deviceType) != inventory.StateDiscovered {
				t.Errorf("%s does not start discovered", deviceType)
			}
		})
	}
	if record.InitialState("linux_server") != inventory.StateActive {
		t.Error("a vendor type no longer starts active")
	}
}

// TestGeneric_RefusesAForeignOrMalformedDiscovery: a discovery naming a
// capability the type may not hold, or one that is not a discovery at
// all, stops the device loading rather than being read as nothing.
func TestGeneric_RefusesAForeignOrMalformedDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value inventory.PropertyValue
	}{
		{"foreign capability", discovery(capability.NameHTTPAPI, capability.NameCiscoIOS)},
		{"not a mapping", "yes"},
		{"capabilities not a list", map[string]any{"capabilities": "HTTPAPICapable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildWith(t, generic.TypeHTTP, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: tc.value}, nil); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// TestNetconf_HasNoCommandLine: however it is discovered, a
// generic_netconf device never satisfies the CLI capability, and its SSH
// port is its NETCONF port unless set.
func TestNetconf_HasNoCommandLine(t *testing.T) {
	item, err := buildWith(t, generic.TypeNetconf, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: discovery(capability.NameNetconf)}, []capability.Name{capability.NameNetworkCLI})
	if err != nil {
		t.Fatal(err)
	}
	if item.HasCapability(capability.NameNetworkCLI) {
		t.Error("a NETCONF device satisfies NetworkCLICapable")
	}
	nc := item.(interface {
		SSHPort() int
		NetconfPort() int
	})
	if nc.SSHPort() != 830 || nc.NetconfPort() != 830 {
		t.Errorf("ports %d and %d, want 830", nc.SSHPort(), nc.NetconfPort())
	}
}

// TestHTTP_RefusesUnusableRecords covers the base URL and credential mode
// rules generic_http enforces through pkg/httpapi.
func TestHTTP_RefusesUnusableRecords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]inventory.PropertyValue
		ok    bool
	}{
		{"https with basic", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://a.example/v2", generic.HTTPAuthProperty: "basic"}, true},
		{"http without a credential", map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://a.example"}, true},
		{"http with a credential", map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://a.example", generic.HTTPAuthProperty: "bearer"}, false},
		{"user information", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://u:p@a.example"}, false},
		{"another scheme", map[string]inventory.PropertyValue{generic.BaseURLProperty: "ftp://a.example"}, false},
		{"a query", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://a.example/?x=1"}, false},
		{"no host", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https:///path"}, false},
		{"unknown credential mode", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://a.example", generic.HTTPAuthProperty: "digest"}, false},
		{"openapi path off the API", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://a.example", generic.OpenAPIPathProperty: "//evil.example/x"}, false},
		{"no base URL", map[string]inventory.PropertyValue{generic.BaseURLProperty: ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildWith(t, generic.TypeHTTP, tc.props, nil)
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestGRPC_RefusesUnusableRecords covers the target and plaintext rules.
func TestGRPC_RefusesUnusableRecords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]inventory.PropertyValue
		ok    bool
	}{
		{"host and port", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "10.0.0.5:50051"}, true},
		{"IPv6", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "[::1]:50051", generic.GRPCPlaintextProperty: true}, true},
		{"no port", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "10.0.0.5"}, false},
		{"port out of range", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "10.0.0.5:70000"}, false},
		{"a resolver scheme", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "dns:///a.example:443"}, false},
		{"a unix socket", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "unix:/run/x.sock"}, false},
		{"plaintext not a boolean", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "a.example:1", generic.GRPCPlaintextProperty: "yes"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildWith(t, generic.TypeGRPC, tc.props, nil)
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestSSH_ReadsWhatOnboardingLearned: the kernel release and distribution
// come from the discovery unless the record sets them.
func TestSSH_ReadsWhatOnboardingLearned(t *testing.T) {
	d := inventory.Discovery{Protocol: "ssh", Capabilities: []capability.Name{capability.NameLinux}, Facts: map[string]any{"kernel_release": "6.1.0", "os_id": "debian"}}
	item, err := buildWith(t, generic.TypeSSH, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	linux := item.(capability.LinuxCapable)
	if linux.KernelVersion() != "6.1.0" || linux.Distribution() != "debian" {
		t.Errorf("kernel %q, distribution %q", linux.KernelVersion(), linux.Distribution())
	}
}

// TestAccessors reads each type's address and settings back from its
// record, with their defaults.
func TestAccessors(t *testing.T) {
	g, err := buildWith(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "10.0.0.5:50051", generic.GRPCPlaintextProperty: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	grpc := g.(interface {
		capability.GRPCCapable
		capability.NetworkAddressableCapable
	})
	if grpc.GRPCTarget() != "10.0.0.5:50051" || !grpc.GRPCPlaintext() || grpc.IPAddress() != "10.0.0.5" {
		t.Errorf("gRPC accessors: %q %v %q", grpc.GRPCTarget(), grpc.GRPCPlaintext(), grpc.IPAddress())
	}

	h, err := buildWith(t, generic.TypeHTTP, map[string]inventory.PropertyValue{generic.HTTPAuthProperty: "bearer", generic.OpenAPIPathProperty: "/openapi.json"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	api := h.(interface {
		capability.HTTPAPICapable
		capability.NetworkAddressableCapable
		OpenAPIPath() string
	})
	if api.HTTPBaseURL() != "https://api.example.com" || api.HTTPAuth() != "bearer" || api.OpenAPIPath() != "/openapi.json" || api.IPAddress() != "api.example.com" {
		t.Errorf("HTTP accessors: %q %q %q %q", api.HTTPBaseURL(), api.HTTPAuth(), api.OpenAPIPath(), api.IPAddress())
	}

	n, err := buildWith(t, generic.TypeNetconf, map[string]inventory.PropertyValue{"port": 2830, "netconf_port": 1830}, nil)
	if err != nil {
		t.Fatal(err)
	}
	nc := n.(interface {
		capability.SSHTransportCapable
		capability.NetconfCapable
		capability.NetworkAddressableCapable
	})
	if nc.SSHHost() != "10.0.0.2" || nc.SSHPort() != 2830 || nc.NetconfPort() != 1830 || nc.IPAddress() != "10.0.0.2" {
		t.Errorf("NETCONF accessors: %q %d %d %q", nc.SSHHost(), nc.SSHPort(), nc.NetconfPort(), nc.IPAddress())
	}
	if _, err := buildWith(t, generic.TypeNetconf, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: "yes"}, nil); err == nil {
		t.Error("a malformed discovery was accepted by generic_netconf")
	}
	if _, err := buildWith(t, generic.TypeGRPC, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: "yes"}, nil); err == nil {
		t.Error("a malformed discovery was accepted by generic_grpc")
	}
	if _, err := buildWith(t, generic.TypeGRPC, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: ":443"}, nil); err == nil {
		t.Error("a target with no host was accepted")
	}
	if generic.Discoverable("linux_server") != nil {
		t.Error("a vendor type has discoverable capabilities")
	}
}

// TestSSH_ARecordSettingWinsOverTheDiscovery: a kernel version the record
// sets is not replaced by what the probe read, and a malformed discovery
// reads as no fact.
func TestSSH_ARecordSettingWinsOverTheDiscovery(t *testing.T) {
	d := inventory.Discovery{Protocol: "ssh", Facts: map[string]any{"kernel_release": "6.1.0", "os_id": "debian"}}
	item, err := buildWith(t, generic.TypeSSH, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property(), "kernel_version": "5.10", "distribution": "custom"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l := item.(capability.LinuxCapable); l.KernelVersion() != "5.10" || l.Distribution() != "custom" {
		t.Errorf("kernel %q, distribution %q", l.KernelVersion(), l.Distribution())
	}
	bare, err := buildWith(t, generic.TypeSSH, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l := bare.(capability.LinuxCapable); l.KernelVersion() != "" {
		t.Errorf("no discovery read kernel %q", l.KernelVersion())
	}
}

// repointed is, per bound type, a change to the address its discovery was
// made against.
var repointed = map[string]map[string]inventory.PropertyValue{
	generic.TypeHTTP: {generic.BaseURLProperty: "https://attacker.example.net"},
	generic.TypeGRPC: {generic.GRPCTargetProperty: "attacker.example.net:443"},
}

// TestGeneric_ARepointedDeviceHoldsOnlyItsBaseline is Phase 117a's S2 at
// the type: a discovery made against one address grants nothing once the
// record names another, and the device still loads and says to onboard it
// again. Changing a TLS setting voids it the same way.
func TestGeneric_ARepointedDeviceHoldsOnlyItsBaseline(t *testing.T) {
	for deviceType, change := range repointed {
		t.Run(deviceType, func(t *testing.T) {
			grants := generic.Discoverable(deviceType)
			for name, extra := range map[string]map[string]inventory.PropertyValue{
				"address": change,
				"tls":     {"tls_server_name": "attacker.example.net"},
			} {
				props := map[string]inventory.PropertyValue{inventory.DiscoveredProperty: boundDiscovery(deviceType, grants...)}
				for k, v := range extra {
					props[k] = v
				}
				item, err := buildWith(t, deviceType, props, nil)
				if err != nil {
					t.Fatalf("%s: a repointed device failed to load: %v", name, err)
				}
				for _, c := range grants {
					if item.HasCapability(c) {
						t.Errorf("%s: a discovery made against another address still grants %s", name, c)
					}
				}
				stale, ok := item.(inventory.StaleDiscoverer)
				if !ok || !strings.Contains(stale.StaleDiscovery(), "pleiades onboard d1") {
					t.Errorf("%s: the device does not say to onboard it again", name)
				}
			}
		})
	}
}

// TestGeneric_AnUnboundDiscoveryGrantsNothingToABoundType: a discovery
// recorded before discoveries were bound grants nothing to a bound type
// (pre-1.0, forward only), and says why; an SSH-based type, which is not
// bound, is unaffected.
func TestGeneric_AnUnboundDiscoveryGrantsNothingToABoundType(t *testing.T) {
	for deviceType := range repointed {
		item, err := buildWith(t, deviceType, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: discovery(generic.Discoverable(deviceType)...)}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if item.HasCapability(generic.Discoverable(deviceType)[0]) {
			t.Errorf("%s: an unbound discovery granted a capability", deviceType)
		}
		if s := item.(inventory.StaleDiscoverer).StaleDiscovery(); !strings.Contains(s, "predates") {
			t.Errorf("%s: the reason %q does not say the discovery predates binding", deviceType, s)
		}
	}
	ssh, err := buildWith(t, generic.TypeSSH, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: discovery(capability.NameShellExec)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ssh.HasCapability(capability.NameShellExec) {
		t.Error("an SSH-based type's discovery became bound; its host-key check already protects it")
	}
}
