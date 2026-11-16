// Package gnmi: what the external tests reach of the package's own state, so
// a test can prove the keepalive bound in seconds rather than minutes and
// read the call timeout a client took.
package gnmi

import (
	"testing"
	"time"
)

// SetKeepalive sets the keepalive ping's time and timeout for t's clients,
// restoring both when t ends. gRPC holds the time to at least ten seconds.
func SetKeepalive(t testing.TB, every, timeout time.Duration) {
	t.Helper()
	oldTime, oldTimeout := keepaliveTime, keepaliveTimeout
	keepaliveTime, keepaliveTimeout = every, timeout
	t.Cleanup(func() { keepaliveTime, keepaliveTimeout = oldTime, oldTimeout })
}

// CallTimeoutOf is the bound c holds each call to.
func CallTimeoutOf(c *Client) time.Duration {
	return c.callTimeout
}
