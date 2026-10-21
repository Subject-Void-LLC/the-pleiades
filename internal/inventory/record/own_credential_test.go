// Tests of the registry of device types that authenticate only with their
// own credential, and of which devices a machine credential may reach.
package record_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
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
			if got := record.TakesMachineCredential(tt.deviceType, tt.caps); got != tt.want {
				t.Errorf("TakesMachineCredential(%q, %v) = %v, want %v", tt.deviceType, tt.caps, got, tt.want)
			}
		})
	}
}
