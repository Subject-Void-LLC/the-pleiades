// Device types that authenticate only with their own stored credential,
// and the devices a template's machine credential may authenticate.
package record

import (
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// ownCredential holds the device types a template's machine credential
// never authenticates.
var ownCredential sync.Map

// RegisterOwnCredentialType marks deviceType as authenticating only with
// the credential stored for each device of it, even where it declares a
// login capability. A template's machine credential (an SSH or network
// login) still reaches every device that can be logged into, AWX's
// semantics; for one of these it would be a login meant for a machine sent
// to an API that is not one, and would win over the device's own
// credential. A Kubernetes cluster was the first such type (Part XVIII
// 119c). TakesMachineCredential now refuses every device with no login
// capability, so a registration only adds to that.
func RegisterOwnCredentialType(deviceType string) {
	ownCredential.Store(deviceType, true)
}

// UsesOwnCredential reports whether deviceType authenticates only with its
// own stored credential.
func UsesOwnCredential(deviceType string) bool {
	_, ok := ownCredential.Load(deviceType)
	return ok
}

// machineLogins are the capabilities a machine credential authenticates:
// a login to a shell, a network device's command line, NETCONF over SSH,
// WinRM, or a console.
var machineLogins = []capability.Name{
	capability.NameSSHTransport,
	capability.NameWinRM,
	capability.NameTelnet,
	capability.NameSerial,
	capability.NameRFC2217,
	capability.NameRawPassthrough,
	capability.NameNetconf,
	capability.NameNetworkCLI,
}

// TakesMachineCredential reports whether a template's machine credential
// may authenticate a device of deviceType declaring caps: only when the
// device can be logged into (machineLogins) and its type is not registered
// as using its own credential.
//
// It is an allowlist on purpose (Phase 117b, S1). A machine credential sent
// to a device reached only through an API (generic_http, a Catalyst Center,
// an AWS account) is the switches' login handed to whatever answers at that
// device's URL, and it also displaces the device's own stored credential. A
// new device type is refused it until it declares a login, rather than given
// it until someone remembers to register the type.
func TakesMachineCredential(deviceType string, caps []capability.Name) bool {
	if UsesOwnCredential(deviceType) {
		return false
	}
	declared := make(map[capability.Name]struct{}, len(caps))
	for _, c := range caps {
		declared[c] = struct{}{}
	}
	for _, login := range machineLogins {
		if capability.Resolves(declared, login) {
			return true
		}
	}
	return false
}
