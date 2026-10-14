// A device's route as a dialer a Collection method can use: the hops
// ResolveRoute names, dialed through pkg/remoteexec's own hop chain.
package engine

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport/hopchain"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// RouteDialerFor resolves device's route (ResolveRoute) and returns a
// dialer through it, and every hop credential's secret values, which the
// caller must add to its masking set; a nil dialer when the device has no
// route.
func RouteDialerFor(ctx context.Context, repo hopChainInventory, credentials credential.Store, device pkginventory.InventoryItem) (sdk.RouteDial, []string, error) {
	route, err := ResolveRoute(ctx, repo, credentials, device)
	if err != nil {
		return nil, nil, err
	}
	if len(route) == 0 {
		return nil, nil, nil
	}
	dial, err := routeDial(route)
	if err != nil {
		return nil, nil, err
	}
	return dial, hopSecrets(route), nil
}

// routeDial is a dialer through route's hops, verifying each hop's host key
// the way every SSH connection is verified (remoteexec.Shared's
// known_hosts), and refusing an address that is not host:port.
func routeDial(route []transport.Hop) (sdk.RouteDial, error) {
	hops, err := hopchain.Convert(route)
	if err != nil {
		return nil, err
	}
	runner := remoteexec.Shared(remoteexec.Options{})
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("a route carries TCP, not %s", network)
		}
		host, portText, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("dialing %q through a route: %w", address, err)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("dialing %q through a route: the port is not a TCP port", address)
		}
		return runner.DialThroughHops(ctx, hops, remoteexec.Target{Host: host, Port: port})
	}, nil
}

// hopSecrets is every secret value route's hop credentials carry, for the
// masking set: a bastion's password is as sensitive as the target's.
func hopSecrets(route []transport.Hop) []string {
	var out []string
	for _, hop := range route {
		out = append(out, hop.Credential.Password, string(hop.Credential.PrivateKeyPEM), hop.Credential.Passphrase)
	}
	return out
}
