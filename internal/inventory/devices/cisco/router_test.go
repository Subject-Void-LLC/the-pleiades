package cisco_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/cisco"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// TestNewRouter_BaselineCapabilities is a regression proof: a Record with
// no classification-derived Capabilities (the explicit-Type hydration
// path, unchanged since before Phase 32) still gets exactly the vendor
// baseline it always did.
func TestNewRouter_BaselineCapabilities(t *testing.T) {
	item, err := cisco.NewRouter(record.Record{ID: "r1", Name: "r1", Type: "cisco_router"})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) {
		t.Error("expected the vendor baseline to include SSHTransportCapable")
	}
	if !item.HasCapability(capability.NameCiscoIOS) {
		t.Error("expected the vendor baseline to include CiscoIOSCapable")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected no capability beyond the vendor baseline with no classification data")
	}
}

// TestNewRouter_UnionsClassificationCapabilities proves Phase 32's
// capability granularity decision at the data layer: classification-
// derived Capabilities add to the vendor baseline in the declared set,
// they never replace it. It deliberately checks Capabilities(), not
// HasCapability(NameApt): Router structurally implements neither
// AptCapable nor PackageManagerCapable's methods, so "neither side is
// trusted alone" means HasCapability(NameApt) correctly stays false here
// even though the data says the device declares it -- classification can
// add data, but it cannot make a concrete type structurally satisfy an
// interface it was never written to satisfy. Phase 33/34's generated
// device types are what eventually closes that structural gap.
func TestNewRouter_UnionsClassificationCapabilities(t *testing.T) {
	rec := record.Record{
		ID:           "r1",
		Name:         "r1",
		Type:         "cisco_router",
		Capabilities: []capability.Name{capability.NameApt},
	}
	item, err := cisco.NewRouter(rec)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) || !item.HasCapability(capability.NameCiscoIOS) {
		t.Error("expected the vendor baseline to survive alongside classification-derived capabilities")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected HasCapability(AptCapable) to stay false: Router does not structurally implement it")
	}

	found := false
	for _, c := range item.Capabilities() {
		if c == capability.NameApt {
			found = true
		}
	}
	if !found {
		t.Error("expected the classification-derived AptCapable to be unioned into the declared set")
	}
}

// TestNewRouter_ResolvesBroadNetworkCLICapable is docs/hephaestus.md's own
// worked example made real: net.cli.config requires the broad
// NetworkCLICapable; a Router only ever declares the narrower
// CiscoIOSCapable, whose Section 8 Parent is NetworkCLICapable. Router
// also structurally implements CLIPrompt() (NetworkCLICapable's method),
// so this resolves true on both the data and structural sides, not just
// one.
func TestNewRouter_ResolvesBroadNetworkCLICapable(t *testing.T) {
	item, err := cisco.NewRouter(record.Record{ID: "r1", Name: "r1", Type: "cisco_router"})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	if !item.HasCapability(capability.NameNetworkCLI) {
		t.Error("expected a Router declaring only CiscoIOSCapable to resolve the broader NetworkCLICapable")
	}
}

// TestRouter_Accessors exercises every structural accessor method Router
// implements to back its capability interfaces, reading through
// Properties() exactly as the real SSH/IOS transport and engine dispatch
// code does.
func TestRouter_Accessors(t *testing.T) {
	rec := record.Record{
		ID:   "r1",
		Name: "r1",
		Type: "cisco_router",
		Properties: map[string]inventory.PropertyValue{
			"host":            "10.0.0.1",
			"port":            8022,
			"ios_version":     "17.3.2",
			"netconf_enabled": true,
			"cli_prompt":      "Router#",
		},
	}
	item, err := cisco.NewRouter(rec)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	router, ok := item.(*cisco.Router)
	if !ok {
		t.Fatalf("NewRouter returned %T, want *cisco.Router", item)
	}

	if got := router.SSHHost(); got != "10.0.0.1" {
		t.Errorf("SSHHost() = %q, want %q", got, "10.0.0.1")
	}
	if got := router.SSHPort(); got != 8022 {
		t.Errorf("SSHPort() = %d, want %d", got, 8022)
	}
	if got := router.IOSVersion(); got != "17.3.2" {
		t.Errorf("IOSVersion() = %q, want %q", got, "17.3.2")
	}
	if !router.SupportsNETCONF() {
		t.Error("SupportsNETCONF() = false, want true")
	}
	if got := router.CLIPrompt(); got != "Router#" {
		t.Errorf("CLIPrompt() = %q, want %q", got, "Router#")
	}
}

// TestRouter_SSHPort_DefaultsTo22 proves the fallback branch when no port
// property is set.
func TestRouter_SSHPort_DefaultsTo22(t *testing.T) {
	item, err := cisco.NewRouter(record.Record{ID: "r1", Name: "r1", Type: "cisco_router"})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	router := item.(*cisco.Router)
	if got := router.SSHPort(); got != 22 {
		t.Errorf("SSHPort() = %d, want default 22", got)
	}
}
