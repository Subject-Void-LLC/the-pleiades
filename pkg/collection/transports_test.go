// Tests for the transport vocabulary check at registration and for
// CheckTransports.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestRegister_RejectsAnUnknownTransport proves a misspelled transport
// fails when the method registers, naming the ones that exist, rather than
// declaring a transport no device can ever reach.
func TestRegister_RejectsAnUnknownTransport(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	d := collection.Descriptor{
		Name:     "test.unknown_transport",
		Manifest: collection.Manifest{SupportedTransports: []string{"ssh", "shh"}},
	}
	err := collection.Register(d)
	if err == nil || !strings.Contains(err.Error(), `"shh"`) || !strings.Contains(err.Error(), "winrm") {
		t.Fatalf("err = %v, want a refusal naming the unknown transport and the known ones", err)
	}
	if _, ok := collection.Lookup(d.Name); ok {
		t.Error("the method registered despite the refusal")
	}
}

// TestCheckTransports covers every answer CheckTransports gives.
func TestCheckTransports(t *testing.T) {
	sshOnly := collection.Manifest{SupportedTransports: []string{capability.TransportSSH}}
	either := collection.Manifest{SupportedTransports: []string{capability.TransportSSH, capability.TransportWinRM}}
	stub := func(caps ...capability.Name) *inventorytest.Stub {
		return &inventorytest.Stub{StubName: "d1", Caps: caps}
	}

	tests := []struct {
		name     string
		device   *inventorytest.Stub
		manifest collection.Manifest
		wantErr  string
	}{
		{"a method declaring no transport has nothing to check", stub(), collection.Manifest{}, ""},
		{"an ssh device reaches an ssh method", stub(capability.NameSSHTransport), sshOnly, ""},
		{"a winrm device reaches a method declaring ssh or winrm", stub(capability.NameWinRM), either, ""},
		{"a winrm device does not reach an ssh method", stub(capability.NameWinRM, capability.NameWindowsShell), sshOnly,
			`collection method "x.y" reaches its device over ssh, and device "d1" reaches only winrm`},
		{"a device reaching two transports names both", stub(capability.NameWinRM, capability.NameHTTPAPI), sshOnly,
			"reaches only https and winrm"},
		{"a device reaching nothing says so", stub(), either,
			`over ssh or winrm, and device "d1" reaches none of the transports this platform knows`},
		{"a catalyst center reaches https", stub(capability.NameCatalystAPI),
			collection.Manifest{SupportedTransports: []string{capability.TransportHTTPS}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := collection.CheckTransports(tt.device, "x.y", tt.manifest)
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

// TestCheckTransports_ANilDeviceHasNothingToReach proves a task with no
// device is left to the execution context to judge, not refused here.
func TestCheckTransports_ANilDeviceHasNothingToReach(t *testing.T) {
	m := collection.Manifest{SupportedTransports: []string{capability.TransportSSH}}
	if err := collection.CheckTransports(nil, "x.y", m); err != nil {
		t.Fatalf("err = %v, want none", err)
	}
}
