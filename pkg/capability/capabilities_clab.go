// Package capability: a containerlab API server, whose REST API the
// virt.clab methods deploy, inspect and change labs through.
package capability

// NameClabAPI is the capability of a containerlab API server.
const NameClabAPI Name = "ContainerlabAPICapable"

// ContainerlabAPICapable is satisfied by a device that is an HTTP API (so it
// has the accessors of HTTPAPICapable) and that onboarding found to be a
// containerlab API server: its unauthenticated /health answers with the
// server's own signature. The capability is the finding and the accessors are
// the way to the API, which is reached with the device's own TLS settings and
// logged in to with its own stored credential, never a template's machine
// credential.
type ContainerlabAPICapable interface {
	HTTPAPICapable
}

func init() {
	Register(Descriptor{
		Name:   NameClabAPI,
		Assert: func(item any) bool { _, ok := item.(ContainerlabAPICapable); return ok },
	})
}
