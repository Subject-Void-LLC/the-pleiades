// A real NATS broker behind a real Toxiproxy, for a test that cuts the link.
//
// It lived in internal/event as natsThroughToxiproxy, with a second inline
// copy in that package's chaos test, until Phase 96c's release gate needed
// the identical topology from internal/runner. A third private copy would
// have been the drift this package exists to end, so the one topology is
// here and all three callers use it.
package testsupport

import (
	"context"
	"testing"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/testcontainers/testcontainers-go"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"
)

// NATSThroughToxiproxy starts a real nats-server and a real Toxiproxy in
// front of it on one Docker network. It returns the broker, whose URL is
// the DIRECT address a test uses for a side of the mesh that is not cut,
// the PROXIED nats:// URL for the side that is, and the live proxy the test
// severs with Disable and heals with Enable. Everything is removed when tb
// finishes.
//
// It takes testing.TB rather than *testing.T so a benchmark can stand up
// the identical topology. Only Helper, Fatalf and Cleanup are used, which
// behave the same on both.
func NATSThroughToxiproxy(tb testing.TB) (*NATSBroker, string, *toxiproxyclient.Proxy) {
	tb.Helper()
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		tb.Fatalf("failed to create network: %v", err)
	}
	tb.Cleanup(func() { _ = nw.Remove(context.Background()) })

	// The alias is what the proxy's upstream resolves, so it is passed
	// explicitly rather than defaulted: "nats:4222" below is this line.
	// The client port is still published on the host too, which is what
	// the returned broker's URL reaches without passing through the proxy.
	broker := StartNATS(tb, WithNATSNetwork(nw, "nats"))

	proxyContainer, err := tctoxiproxy.Run(ctx,
		ToxiproxyImage,
		tctoxiproxy.WithProxy("nats", "nats:4222"),
		network.WithNetwork([]string{"toxiproxy"}, nw),
		ToxiproxyReady(),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(proxyContainer) // a failed start still returns its container
		tb.Fatalf("failed to start toxiproxy container: %v", err)
	}
	tb.Cleanup(func() { _ = proxyContainer.Terminate(context.Background()) })

	proxiedHost, proxiedPort, err := proxyContainer.ProxiedEndpoint(8666)
	if err != nil {
		tb.Fatalf("failed to get proxied endpoint: %v", err)
	}
	controlURI, err := proxyContainer.URI(ctx)
	if err != nil {
		tb.Fatalf("failed to get toxiproxy control URI: %v", err)
	}
	proxies, err := toxiproxyclient.NewClient(controlURI).Proxies()
	if err != nil {
		tb.Fatalf("failed to list proxies: %v", err)
	}
	proxy, ok := proxies["nats"]
	if !ok {
		tb.Fatal("toxiproxy has no \"nats\" proxy registered")
	}
	return broker, "nats://" + proxiedHost + ":" + proxiedPort, proxy
}
