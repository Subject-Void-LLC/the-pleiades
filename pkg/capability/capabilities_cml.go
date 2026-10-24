// Package capability: a Cisco Modeling Labs controller, whose REST API the
// virt.cml methods build and remove labs through.
package capability

// NameCMLAPI is the capability of a CML 2 controller's REST API.
const NameCMLAPI Name = "CMLAPICapable"

// CMLAPICapable is satisfied by a Cisco Modeling Labs 2 controller: its
// REST API (/api/v0), reached over HTTPS with the device's own TLS settings
// and logged in to with the device's own credential, never a template's
// machine credential. Its console server, an SSH service on the same host,
// is how a node with no address of its own is reached.
type CMLAPICapable interface {
	// CMLBaseURL returns the controller's base URL, e.g.
	// "https://10.10.20.161".
	CMLBaseURL() string
	// CMLPlaintextWarning returns, when the record allows the login over
	// plain HTTP, the warning every run shows, and "" otherwise.
	CMLPlaintextWarning() string
	// CMLConsoleAddress returns the host and port of the controller's
	// console server.
	CMLConsoleAddress() (string, int)
}

func init() {
	Register(Descriptor{
		Name:   NameCMLAPI,
		Assert: func(item any) bool { _, ok := item.(CMLAPICapable); return ok },
	})
}
