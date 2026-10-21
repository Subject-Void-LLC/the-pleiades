// Tests of the registry of device types that authenticate only with their
// own credential, and of which devices a machine credential may reach.
package record_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/catalyst"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestUsesOwnCredential: a registered type uses its own credential; any
// other type, registered as nothing or unknown, does not.
func TestUsesOwnCredential(t *testing.T) {
	record.RegisterOwnCredentialType("record_test_own_credential")
	if !record.UsesOwnCredential("record_test_own_credential") {
		t.Error("a registered type does not use its own credential")
	}
	for _, typ := range []string{"record_test_unregistered", ""} {
		if record.UsesOwnCredential(typ) {
			t.Errorf("%q uses its own credential, never having been registered", typ)
		}
	}
}

// TestTakesMachineCredential: a machine credential authenticates a device
// only through a login (SSH, WinRM, a console, NETCONF or a network command
// line, a child capability counting as its parent), never a device reached
// only through an API, and never a type registered as using its own.
func TestTakesMachineCredential(t *testing.T) {
	record.RegisterOwnCredentialType("record_test_own_login")
	tests := []struct {
		name       string
		deviceType string
		caps       []capability.Name
		want       bool
	}{
		{"a server over SSH", "linux_server", []capability.Name{capability.NameLinux, capability.NameSSHTransport}, true},
		{"a Windows server over WinRM", "windows_server", []capability.Name{capability.NameWinRM}, true},
		{"a console over Telnet", "console", []capability.Name{capability.NameTelnet}, true},
		{"a serial line", "console", []capability.Name{capability.NameSerial}, true},
		{"a NETCONF device", "generic_netconf", []capability.Name{capability.NameNetconf}, true},
		{"a Junos command line, a child of the network CLI", "junos", []capability.Name{capability.NameJunos}, true},
		{"an HTTP API", "generic_http", []capability.Name{capability.NameHTTPAPI, capability.NameNetworkAddressable}, false},
		{"a gRPC server", "generic_grpc", []capability.Name{capability.NameGRPC, capability.NameNetworkAddressable}, false},
		{"a Catalyst Center", "catalyst_center", []capability.Name{capability.NameCatalystAPI}, false},
		{"an AWS account", "aws_account", []capability.Name{capability.NameAWSAPI}, false},
		{"a Kubernetes cluster", "kubernetes_cluster", []capability.Name{capability.NameKubernetes}, false},
		{"a container engine over TLS alone", "container_host", []capability.Name{capability.NameDocker, capability.NameEngineTLS}, false},
		{"a device declaring nothing", "", nil, false},
		{"a login on a type registered as using its own credential", "record_test_own_login", []capability.Name{capability.NameSSHTransport}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			device := &inventorytest.Stub{StubName: "d1", Caps: tt.caps}
			if got := record.TakesMachineCredential(tt.deviceType, device); got != tt.want {
				t.Errorf("TakesMachineCredential(%q, %v) = %v, want %v", tt.deviceType, tt.caps, got, tt.want)
			}
		})
	}
	if record.TakesMachineCredential("linux_server", nil) {
		t.Error("a nil device takes the machine credential")
	}
}

// TestTakesMachineCredential_ADeclarationAloneOpensNothing: a Catalyst
// Center's record may add capabilities to its type's baseline, so one
// declaring SSHTransportCapable lists it among its capabilities. The type
// implements no SSH accessor, so HasCapability says no, and the machine
// credential is still refused: whoever writes inventory cannot route the
// switches' login to an API by declaring a login.
func TestTakesMachineCredential_ADeclarationAloneOpensNothing(t *testing.T) {
	device, err := catalyst.NewCenter(record.Record{
		ID: "cc-id", Name: "cc1", Type: "catalyst_center", State: inventory.StateActive,
		Properties:   map[string]inventory.PropertyValue{"host": "cc.example.test"},
		Capabilities: []capability.Name{capability.NameSSHTransport},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(device.Capabilities(), capability.NameSSHTransport) {
		t.Fatalf("capabilities %v: the record's declaration did not reach the declared set, so this proves nothing", device.Capabilities())
	}
	if record.TakesMachineCredential("catalyst_center", device) {
		t.Error("a Catalyst Center declaring SSHTransportCapable in its record takes the machine credential")
	}
}
