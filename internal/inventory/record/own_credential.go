// Device types that authenticate only with their own stored credential,
// and the devices a template's machine credential may authenticate.
package record

import (
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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
// may authenticate device, of deviceType: only when the device can be
// logged into (machineLogins) and its type is not registered as using its
// own credential. A nil device takes nothing.
//
// It asks device.HasCapability, never the declared list alone. A vendor
// type unions a record's own capabilities into its declared set, so a
// record writer could declare SSHTransportCapable on a Catalyst Center;
// HasCapability also asserts the Go type implements the capability, which
// that type does not, so a declaration cannot open the credential to it.
//
// It is an allowlist on purpose (Phase 117b, S1). A machine credential sent
// to a device reached only through an API (generic_http, a Catalyst Center,
// an AWS account) is the switches' login handed to whatever answers at that
// device's URL, and it also displaces the device's own stored credential. A
// new device type is refused it until it declares a login, rather than given
// it until someone remembers to register the type.
func TakesMachineCredential(deviceType string, device inventory.InventoryItem) bool {
	if device == nil || UsesOwnCredential(deviceType) {
		return false
	}
	for _, login := range machineLogins {
		if device.HasCapability(login) {
			return true
		}
	}
	return false
}
