// Device types that authenticate only with their own stored credential.
package record

import "sync"

// ownCredential holds the device types a template's machine credential
// never authenticates.
var ownCredential sync.Map

// RegisterOwnCredentialType marks deviceType as authenticating only with
// the credential stored for each device of it. A template's machine
// credential (an SSH or network login) still reaches every other device in
// a fan-out, AWX's semantics; for one of these it would be a login meant
// for a machine sent to an API that is not one, and would win over the
// device's own credential. A Kubernetes cluster is the first such type
// (Part XVIII 119c), until Phase 117c's slots bind a credential per device.
func RegisterOwnCredentialType(deviceType string) {
	ownCredential.Store(deviceType, true)
}

// UsesOwnCredential reports whether deviceType authenticates only with its
// own stored credential.
func UsesOwnCredential(deviceType string) bool {
	_, ok := ownCredential.Load(deviceType)
	return ok
}
