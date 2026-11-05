// net.ssh.ping on a device reached through a route of SSH hops dials through
// the route's dialer (sdk.Router), never the device's address.
package ssh_test

import (
	"context"
	"net"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ssh"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// routedStubContext is a stubContext whose device is reached through a
// route: its dialer reaches the server whatever address it is asked for.
type routedStubContext struct {
	*stubContext
	server testServer
	dials  int
}

func (c *routedStubContext) DeviceRoute() sdk.RouteDial {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		c.dials++
		var d net.Dialer
		return d.DialContext(ctx, network, c.server.addr())
	}
}

// TestPing_ARoutedDeviceIsPingedThroughItsRoute: the device's address is one
// nothing listens on, so only the route reaches the server.
func TestPing_ARoutedDeviceIsPingedThroughItsRoute(t *testing.T) {
	server := newTestSSHServer(t, "admin", "hunter2", 0)
	device := &sshStub{host: "127.0.0.1", port: 1}
	rc := &routedStubContext{stubContext: pingContext(map[string]string{"username": "admin", "password": "hunter2"}), server: server}
	if _, err := ssh.Ping(context.Background(), rc, device, map[string]any{"insecure_skip_host_key_verify": true}); err != nil {
		t.Fatalf("Ping through the route: %v", err)
	}
	if rc.dials != 1 {
		t.Fatalf("the route dialed %d times, want 1", rc.dials)
	}
	if got := rc.stats["reply"]; got != "pong" {
		t.Errorf("reply stat = %v, want %q", got, "pong")
	}
}
