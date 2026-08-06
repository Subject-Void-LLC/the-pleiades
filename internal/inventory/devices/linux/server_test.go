package linux_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

// TestNewServer_BaselineCapabilities is a regression proof: a Record with
// no classification-derived Capabilities (the explicit-Type hydration
// path, unchanged since before Phase 32) still gets exactly the vendor
// baseline it always did.
func TestNewServer_BaselineCapabilities(t *testing.T) {
	item, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) {
		t.Error("expected the vendor baseline to include SSHTransportCapable")
	}
	if !item.HasCapability(capability.NameLinux) {
		t.Error("expected the vendor baseline to include LinuxCapable")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected no capability beyond the vendor baseline with no classification data")
	}
}

// TestNewServer_UnionsClassificationCapabilities mirrors
// cisco.TestNewRouter_UnionsClassificationCapabilities: classification-
// derived Capabilities are unioned into the declared set at the data
// layer, but HasCapability(NameApt) correctly stays false since Server
// does not structurally implement AptCapable's methods -- "neither side
// is trusted alone."
func TestNewServer_UnionsClassificationCapabilities(t *testing.T) {
	rec := record.Record{
		ID:           "s1",
		Name:         "s1",
		Type:         "linux_server",
		Capabilities: []capability.Name{capability.NameApt},
	}
	item, err := linux.NewServer(rec)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if !item.HasCapability(capability.NameSSHTransport) || !item.HasCapability(capability.NameLinux) {
		t.Error("expected the vendor baseline to survive alongside classification-derived capabilities")
	}
	if item.HasCapability(capability.NameApt) {
		t.Error("expected HasCapability(AptCapable) to stay false: Server does not structurally implement it")
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

// TestServer_Accessors exercises every structural accessor method Server
// implements to back its capability interfaces, reading through
// Properties() exactly as the real SSH transport code does.
func TestServer_Accessors(t *testing.T) {
	rec := record.Record{
		ID:   "s1",
		Name: "s1",
		Type: "linux_server",
		Properties: map[string]inventory.PropertyValue{
			"host":           "10.0.0.2",
			"port":           2222,
			"kernel_version": "6.6.1",
			"distribution":   "ubuntu",
		},
	}
	item, err := linux.NewServer(rec)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server, ok := item.(*linux.Server)
	if !ok {
		t.Fatalf("NewServer returned %T, want *linux.Server", item)
	}

	if got := server.SSHHost(); got != "10.0.0.2" {
		t.Errorf("SSHHost() = %q, want %q", got, "10.0.0.2")
	}
	if got := server.SSHPort(); got != 2222 {
		t.Errorf("SSHPort() = %d, want %d", got, 2222)
	}
	if got := server.KernelVersion(); got != "6.6.1" {
		t.Errorf("KernelVersion() = %q, want %q", got, "6.6.1")
	}
	if got := server.Distribution(); got != "ubuntu" {
		t.Errorf("Distribution() = %q, want %q", got, "ubuntu")
	}
}

// TestServer_SSHPort_DefaultsTo22 proves the fallback branch when no port
// property is set.
func TestServer_SSHPort_DefaultsTo22(t *testing.T) {
	item, err := linux.NewServer(record.Record{ID: "s1", Name: "s1", Type: "linux_server"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server := item.(*linux.Server)
	if got := server.SSHPort(); got != 22 {
		t.Errorf("SSHPort() = %d, want default 22", got)
	}
}
