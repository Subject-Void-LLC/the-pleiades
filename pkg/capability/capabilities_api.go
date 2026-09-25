// The capabilities of a device reached through an API rather than a
// terminal: an HTTP API and gRPC.
package capability

const (
	NameHTTPAPI Name = "HTTPAPICapable"
	NameGRPC    Name = "GRPCCapable"
)

// HTTPAPICapable is satisfied by a device that answers an HTTP API at one
// base URL. http.request joins a relative URL to it and authenticates with
// the device's own stored credential, never one from the runbook.
//
// It is a sibling of CatalystAPICapable and AWSAPICapable rather than
// their parent: those name a specific vendor's API and its accessors, and
// a device answering some HTTP API answers neither.
type HTTPAPICapable interface {
	// HTTPBaseURL returns the API's base URL: http or https, with a host,
	// and with no user information, query or fragment.
	HTTPBaseURL() string

	// HTTPAuth returns how the device's stored credential is sent: "basic"
	// (username and password), "bearer" (the password as the token), or
	// "none".
	HTTPAuth() string
}

// GRPCCapable is satisfied by a device that serves gRPC at one target.
type GRPCCapable interface {
	// GRPCTarget returns the server's address as host:port.
	GRPCTarget() string

	// GRPCPlaintext reports whether the connection is made without TLS.
	// It is false unless the device's record says otherwise, and a stored
	// credential is never sent over a plaintext connection.
	GRPCPlaintext() bool
}

func init() {
	Register(Descriptor{
		Name:   NameHTTPAPI,
		Assert: func(item any) bool { _, ok := item.(HTTPAPICapable); return ok },
	})
	Register(Descriptor{
		Name:   NameGRPC,
		Assert: func(item any) bool { _, ok := item.(GRPCCapable); return ok },
	})
}
