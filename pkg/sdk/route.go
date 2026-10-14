// A device's route through SSH hops, for a method that dials an address
// of its own.
package sdk

import (
	"context"
	"net"
)

// RouteDial dials an address through a device's route.
type RouteDial func(ctx context.Context, network, address string) (net.Conn, error)

// Router is implemented by a RunbookContext that can say how its device is
// reached through a route of SSH hops (a bastion, Phase 72). A method that
// opens a connection of its own rather than through Connect (a Kubernetes
// API server's address) dials it with DeviceRoute's function, so it goes
// through the same hops, verified the same way, as an SSH task would.
type Router interface {
	// DeviceRoute returns a dialer through the device's hops, or nil when
	// the device has no route and is dialed directly.
	DeviceRoute() RouteDial
}
