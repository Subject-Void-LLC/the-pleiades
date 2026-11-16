// Tests for the generic device types: that only a discovery grants a
// capability beyond the baseline, that every discoverable capability is
// really implemented, and that each type refuses an unusable record.
package generic_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// addressed is the least each type needs to build.
var addressed = map[string]map[string]inventory.PropertyValue{
	generic.TypeSSH:      {"host": "10.0.0.1"},
	generic.TypeNetconf:  {"host": "10.0.0.2"},
	generic.TypeHTTP:     {generic.BaseURLProperty: "https://api.example.com"},
	generic.TypeRestconf: {generic.BaseURLProperty: "https://router.example.com"},
	generic.TypeSNMP:     {generic.HostProperty: "10.0.0.3"},
	generic.TypeGRPC:     {generic.GRPCTargetProperty: "grpc.example.com:443"},
	generic.TypeGNMI:     {generic.GRPCTargetProperty: "router.example.com:57400"},
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

// TestRestconf_RefusesUnusableRecords covers the origin, credential and TLS
// rules generic_restconf enforces, with basic authentication its default.
func TestRestconf_RefusesUnusableRecords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]inventory.PropertyValue
		ok    bool
	}{
		{"https with the default credential", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://r.example:8443"}, true},
		{"https with a trailing slash", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://r.example/"}, true},
		{"https with bearer", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://r.example", generic.HTTPAuthProperty: "bearer"}, true},
		{"http without a credential", map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://r.example", generic.HTTPAuthProperty: "none"}, true},
		{"http with the default credential", map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://r.example"}, false},
		{"http with the opt-in", map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://r.example", "http_allow_plaintext_credentials": true}, true},
		{"an opt-in not a boolean", map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://r.example", "http_allow_plaintext_credentials": "yes"}, false},
		{"a path", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://r.example/restconf"}, false},
		{"user information", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://u:p@r.example"}, false},
		{"unknown credential mode", map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://r.example", generic.HTTPAuthProperty: "digest"}, false},
		{"a TLS setting over http", map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://r.example", generic.HTTPAuthProperty: "none", "tls_server_name": "r"}, false},
		{"no base URL", map[string]inventory.PropertyValue{generic.BaseURLProperty: ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildWith(t, generic.TypeRestconf, tc.props, nil)
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

// TestGNMI_RefusesUnusableRecords covers the credential mode, the two
// plaintext opt-ins, the TLS settings gRPC cannot use, and gnmi_target.
func TestGNMI_RefusesUnusableRecords(t *testing.T) {
	const target = "router.example.com:57400"
	for _, tc := range []struct {
		name  string
		props map[string]inventory.PropertyValue
		ok    bool
	}{
		{"defaults", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target}, true},
		{"bearer", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GNMIAuthProperty: "bearer"}, true},
		{"plaintext, no credential", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCPlaintextProperty: true, generic.GNMIAuthProperty: "none"}, true},
		{"plaintext with a credential, accepted", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCPlaintextProperty: true, generic.GRPCAllowPlaintextCredentialsProperty: true}, true},
		{"no target", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: ""}, false},
		{"a resolver scheme", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "dns:///router:57400"}, false},
		{"unknown auth", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GNMIAuthProperty: "basic"}, false},
		{"auth not text", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GNMIAuthProperty: true}, false},
		{"plaintext not a boolean", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCPlaintextProperty: "yes"}, false},
		{"allow not a boolean", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCAllowPlaintextCredentialsProperty: "yes"}, false},
		{"allow without plaintext", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCAllowPlaintextCredentialsProperty: true}, false},
		{"allow with no credential", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCPlaintextProperty: true, generic.GNMIAuthProperty: "none", generic.GRPCAllowPlaintextCredentialsProperty: true}, false},
		{"TLS on plaintext", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GRPCPlaintextProperty: true, devicetls.ServerNameProperty: "r"}, false},
		{"deprecated TLS", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true}, false},
		{"a prefix target", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GNMITargetProperty: "OC-YANG"}, true},
		{"a prefix target that is a path", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GNMITargetProperty: "OC-YANG/interfaces"}, false},
		{"an empty prefix target", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GNMITargetProperty: ""}, false},
		{"a prefix target not text", map[string]inventory.PropertyValue{generic.GRPCTargetProperty: target, generic.GNMITargetProperty: 7}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildWith(t, generic.TypeGNMI, tc.props, nil)
			if (err == nil) != tc.ok {
				t.Fatalf("err %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestGNMI_Accessors reads each accessor a gNMI client is built from.
func TestGNMI_Accessors(t *testing.T) {
	item, err := buildWith(t, generic.TypeGNMI, map[string]inventory.PropertyValue{
		generic.GRPCTargetProperty: "10.0.0.9:57400", generic.GRPCPlaintextProperty: true, generic.GRPCAllowPlaintextCredentialsProperty: true,
		inventory.DiscoveredProperty: boundDiscoveryWith(generic.TypeGNMI, map[string]inventory.PropertyValue{
			generic.GRPCTargetProperty: "10.0.0.9:57400", generic.GRPCPlaintextProperty: true, generic.GRPCAllowPlaintextCredentialsProperty: true,
		}, capability.NameGNMI),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := item.(capability.GNMICapable)
	if !ok || !item.HasCapability(capability.NameGNMI) {
		t.Fatalf("%T is not GNMICapable after its discovery", item)
	}
	if g.GNMITarget() != "10.0.0.9:57400" || g.GNMIAuth() != "password" || !g.GNMIPlaintext() || !g.GNMIAllowPlaintextCredentials() {
		t.Errorf("accessors = %s %s %v %v", g.GNMITarget(), g.GNMIAuth(), g.GNMIPlaintext(), g.GNMIAllowPlaintextCredentials())
	}
	if addr, ok := item.(capability.NetworkAddressableCapable); !ok || addr.IPAddress() != "10.0.0.9" {
		t.Error("IPAddress is not the target's host")
	}
	if targeted, ok := item.(gnmi.PrefixTargeted); !ok || targeted.GNMIPrefixTarget() != "" {
		t.Error("a record naming no gnmi_target answers with one")
	}
	named, err := buildWith(t, generic.TypeGNMI, map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "10.0.0.9:57400", generic.GNMITargetProperty: "OC-YANG"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if targeted, ok := named.(gnmi.PrefixTargeted); !ok || targeted.GNMIPrefixTarget() != "OC-YANG" {
		t.Error("gnmi_target is not the device's prefix target")
	}
	if _, ok := item.(capability.GRPCCapable); ok {
		t.Error("generic_gnmi answers generic_grpc's accessors, which its discovery never proves")
	}
	if configured, ok := item.(devicetls.Configured); !ok || configured.TLSSettings().PinnedCA() {
		t.Error("TLSSettings is missing, or pins an authority the record never named")
	}
	binder, ok := item.(inventory.DiscoveryBinder)
	if !ok || binder.DiscoveryBinding() == "" || binder.DiscoveryBinding() != generic.Binding(generic.TypeGNMI, item.Properties()) {
		t.Error("generic_gnmi's discovery is not bound to its record")
	}
	if _, err := buildWith(t, generic.TypeGNMI, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: "yes"}, nil); err == nil {
		t.Error("a discovery that is not one was accepted")
	}
}

// boundDiscoveryWith is boundDiscovery against props rather than the
// type's address alone.
func boundDiscoveryWith(deviceType string, props map[string]inventory.PropertyValue, caps ...capability.Name) inventory.PropertyValue {
	d := inventory.Discovery{Protocol: "test", Capabilities: caps}
	d.Binding = generic.Binding(deviceType, inventory.NewProperties(props))
	return d.Property()
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

// TestSSH_ReadsTheRoutingToolsOnboardingFound: FRR's and Open vSwitch's
// versions come from the probe's facts, and each capability is granted
// only by that discovery.
func TestSSH_ReadsTheRoutingToolsOnboardingFound(t *testing.T) {
	d := inventory.Discovery{Protocol: "ssh", Capabilities: []capability.Name{capability.NameFRR, capability.NameOpenVSwitch},
		Facts: map[string]any{generic.FactFRRVersion: "10.1.1", generic.FactOVSVersion: "3.3.0"}}
	item, err := buildWith(t, generic.TypeSSH, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !item.HasCapability(capability.NameFRR) || !item.HasCapability(capability.NameOpenVSwitch) {
		t.Fatalf("capabilities = %v, want FRR and Open vSwitch granted", item.Capabilities())
	}
	frr := item.(capability.FRRCapable)
	ovs := item.(capability.OpenVSwitchCapable)
	if frr.FRRVersion() != "10.1.1" || ovs.OVSVersion() != "3.3.0" {
		t.Errorf("FRR %q, Open vSwitch %q", frr.FRRVersion(), ovs.OVSVersion())
	}
}

// TestSSH_ReadsTheFusionVersionOnboardingFound: a Mac's VMware Fusion
// version comes from the probe's facts, and VMwareFusionCapable is granted only
// by that discovery.
func TestSSH_ReadsTheFusionVersionOnboardingFound(t *testing.T) {
	d := inventory.Discovery{Protocol: "ssh", Capabilities: []capability.Name{capability.NameVMwareFusion},
		Facts: map[string]any{generic.FactFusionVersion: "26.0.1"}}
	item, err := buildWith(t, generic.TypeSSH, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !item.HasCapability(capability.NameVMwareFusion) {
		t.Fatalf("capabilities = %v, want VMwareFusionCapable granted", item.Capabilities())
	}
	if v := item.(capability.VMwareFusionCapable).FusionVersion(); v != "26.0.1" {
		t.Errorf("FusionVersion = %q", v)
	}
	plain, err := buildWith(t, generic.TypeSSH, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.HasCapability(capability.NameVMwareFusion) {
		t.Error("a device onboarding never found Fusion on holds VMwareFusionCapable")
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

	r, err := buildWith(t, generic.TypeRestconf, map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://router.example.com:8443/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rc := r.(interface {
		capability.RestconfCapable
		capability.NetworkAddressableCapable
	})
	if rc.RestconfBaseURL() != "https://router.example.com:8443/" || rc.RestconfAuth() != "basic" || rc.RestconfAllowPlaintextCredentials() || rc.IPAddress() != "router.example.com" {
		t.Errorf("RESTCONF accessors: %q %q %v %q", rc.RestconfBaseURL(), rc.RestconfAuth(), rc.RestconfAllowPlaintextCredentials(), rc.IPAddress())
	}
	named, err := buildWith(t, generic.TypeRestconf, map[string]inventory.PropertyValue{"tls_server_name": "router.internal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := named.(devicetls.Configured).TLSSettings().ServerName; got != "router.internal" {
		t.Errorf("TLSSettings().ServerName = %q, want the record's tls_server_name", got)
	}
	binder := named.(inventory.DiscoveryBinder)
	if binder.DiscoveryBinding() == "" || binder.DiscoveryBinding() != generic.Binding(generic.TypeRestconf, named.Properties()) {
		t.Error("generic_restconf's discovery binding is not its bound properties' digest")
	}
	if _, err := buildWith(t, generic.TypeRestconf, map[string]inventory.PropertyValue{inventory.DiscoveredProperty: "yes"}, nil); err == nil {
		t.Error("a malformed discovery was accepted by generic_restconf")
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
	// A vendor type's capabilities come from its Go type, except the
	// container engines a linux_server runs, which only onboarding proves
	// (Phase 119b); a vendor type with no engines has nothing discoverable.
	if got := generic.Discoverable("linux_server"); !slices.Equal(got, engines.SSHGrants) {
		t.Errorf("linux_server's discoverable capabilities = %v, want its engines alone", got)
	}
	if generic.Discoverable("cisco_router") != nil {
		t.Error("a vendor type with no engine has discoverable capabilities")
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
	generic.TypeHTTP:     {generic.BaseURLProperty: "https://attacker.example.net"},
	generic.TypeRestconf: {generic.BaseURLProperty: "https://attacker.example.net"},
	generic.TypeGRPC:     {generic.GRPCTargetProperty: "attacker.example.net:443"},
	generic.TypeGNMI:     {generic.GRPCTargetProperty: "attacker.example.net:57400"},
	generic.TypeSNMP:     {generic.HostProperty: "10.0.0.66"},
}

// TestGeneric_ARepointedDeviceHoldsOnlyItsBaseline is Phase 117a's S2 at
// the type: a discovery made against one address grants nothing once the
// record names another, and the device still loads and says to onboard it
// again. Changing a TLS setting voids it the same way, and for generic_snmp
// changing its SNMP version.
func TestGeneric_ARepointedDeviceHoldsOnlyItsBaseline(t *testing.T) {
	for deviceType, change := range repointed {
		t.Run(deviceType, func(t *testing.T) {
			grants := generic.Discoverable(deviceType)
			second := map[string]inventory.PropertyValue{"tls_server_name": "attacker.example.net"}
			if deviceType == generic.TypeSNMP {
				// generic_snmp has no TLS; a discovery made in one SNMP
				// version says nothing about another.
				second = map[string]inventory.PropertyValue{"snmp_version": "2c", "snmp_allow_cleartext_community": true}
			}
			for name, extra := range map[string]map[string]inventory.PropertyValue{
				"address":   change,
				"transport": second,
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
