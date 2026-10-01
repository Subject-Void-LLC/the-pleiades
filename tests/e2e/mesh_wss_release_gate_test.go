//go:build integration

// This file is Phase 96d's Release Gate, and it exists because the gate it
// replaces proved the wrong thing.
//
// internal/topology/transport_gate_test.go already reaches a real broker
// over ws:// and tls://. What it reaches is a DIRECT LISTENER, dialed by
// the test process. That proves the client speaks the protocol. It does
// not prove traversal, and traversal is the entire claim wss:// makes.
//
// The claim, stated precisely, because the wrong version of it is the kind
// of thing that gets repeated into a datasheet. WebSocket does NOT make a
// connection tolerate link drops. It runs over TCP, a reset kills it
// identically, and reconnecting costs a TLS handshake plus an HTTP upgrade,
// which is strictly MORE work than plain nats://. What it genuinely buys is
// reaching a broker on 443 through an HTTP proxy, a corporate egress filter
// or a CDN or ingress that terminates TLS, which matters a great deal for a
// Runner on somebody else's network and is a completely different property.
// Nothing in this file asserts resilience; internal/event's reconnect gate
// owns that, and owns it for every transport equally.
//
// So the fixture here is the claim: a real nats-server with a real
// websocket block and NO published port at all, a real nginx terminating
// real TLS in front of it, and the real controller and runner binaries
// reaching it over wss:// with NATS_CA_FILE. Toxiproxy, which stands at
// every other network boundary in this repository, deliberately does not
// appear: it forwards TCP bytes, so a gate built on it would restate what
// the direct listener already proves and call it traversal.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// brokerWebSocketPort is the port the broker's websocket listener binds
// inside its container, and the port nginx proxies to.
//
// It is 8080 rather than 443 because TLS is terminated in FRONT of it:
// the broker itself serves plain HTTP here, which is what "terminating
// proxy" means and what the chart renders for
// nats.websocket.enabled=true with nats.websocket.tls=false.
const brokerWebSocketPort = "8080"

// brokerMonitorPort is the broker's own monitoring listener, which
// testsupport.NATSCommand already turns on with -m. The gate reaches it
// THROUGH the proxy, which is what lets the broker container publish
// nothing.
const brokerMonitorPort = "8222"

// proxiedBroker is a real broker with a real terminating proxy in front of
// it, and the handles the gate needs to interrogate both.
type proxiedBroker struct {
	nats  testcontainers.Container
	nginx testcontainers.Container
	// addr is the proxy's host-side address, which is what every client in
	// this mesh dials.
	addr string
}

// TestMeshTransportReleaseGate_TheRealBinariesReachTheBrokerOverWSSThroughATerminatingProxy
// is the gate.
//
// Read the assertions in order: the first is that real work completed, and
// every one after it is about HOW it got there.
func TestMeshTransportReleaseGate_TheRealBinariesReachTheBrokerOverWSSThroughATerminatingProxy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the wss:// traversal gate in short mode")
	}
	ctx := context.Background()

	var proxy *proxiedBroker
	h := startHarness(t, withBrokerBehindTLSProxy(&proxy))

	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	token := issuer.Token(t, &auth.Identity{Subject: "e2e-wss", Role: auth.RoleAdmin})

	// PROOF ZERO, and the load-bearing one: real work completes. A launch
	// against the real controller, a fan-out over the real broker,
	// execution in the real runner, and the result read back. Everything
	// below is about the route it took; this is that it took one.
	h.completeOneDispatch(t, token, "over wss:// through a terminating proxy")

	// PROOF ONE, and a property of the fixture rather than an assertion
	// about behavior, which is what makes it the one proof here that
	// cannot be satisfied by accident: this host has no route to the
	// broker's plain NATS client port, so nothing in this mesh can have
	// skipped the proxy by dialing it.
	//
	// It is asserted rather than assumed because the assumption is one
	// line away from being false in two different ways, and both were
	// found by writing this down. integration_chaos_test.go carried the
	// identical claim in a comment while declaring ExposedPorts anyway.
	// And an empty ExposedPorts, which is what "publish nothing" looks
	// like, in fact publishes every port the IMAGE declares, which for
	// the nats image is all three. See startBrokerWithWebSocket.
	ports, err := proxy.nats.Ports(ctx)
	if err != nil {
		t.Fatalf("inspecting the broker's published ports: %v", err)
	}
	// Ranged rather than indexed, so this file needs no import of the
	// docker API's own port type for a comparison a string already makes.
	unreachable := map[string]string{
		"4222":            "the plain NATS client port",
		"6222":            "the cluster route port",
		brokerMonitorPort: "the monitoring port, which this gate reaches through the proxy on purpose",
	}
	sawExpectedPort := false
	for published, bindings := range ports {
		// Docker's port map can list every port the image EXPOSEs, bound
		// to the host or not, and an exposed port with no binding is no
		// route from this host. The hosted runner's daemon (28.0.4) listed
		// 4222 that way, with no bindings, where a developer's (29.8.0) did
		// not, and reading the map's keys as "published" failed this gate
		// there and nowhere else (FAILURE_PATTERNS 421).
		if len(bindings) == 0 {
			continue
		}
		if published.Port() == brokerWebSocketPort {
			sawExpectedPort = true
		}
		if what, forbidden := unreachable[published.Port()]; forbidden {
			t.Fatalf("the broker container published %s (%s) on %v. That is a second route from this host straight to the broker, bypassing the proxy entirely, and with one in place every assertion in this file would pass without proving traversal", published.Port(), what, bindings)
		}
	}
	// The positive control for the loop above, which is a loop over what
	// must NOT be there: an empty or unreadable port map would satisfy it
	// without checking anything, and would look exactly like a fixture
	// that is correctly locked down. The one port the request does name
	// has to be present.
	if !sawExpectedPort {
		t.Fatalf("the broker published no mapping for its websocket port %s, so the port map this rule just scanned is empty or unreadable and the absence it asserts proves nothing: %v", brokerWebSocketPort, ports)
	}

	// PROOF TWO: the proxy itself reports a 101 Switching Protocols.
	//
	// Proof one says nothing else can have happened. This says the thing
	// that did happen was an HTTP upgrade handshake terminated HERE, which
	// is the one observation that distinguishes a terminating L7 proxy
	// from a byte forwarder.
	//
	// It needs a connection of its own, and the reason is nginx's logging
	// model rather than a preference: an access log line is written when a
	// request ENDS, and a websocket tunnel does not end while the mesh is
	// running. So this opens one, round-trips it, and closes it, which is
	// what makes the line appear while the test can still read it.
	probe, err := nats.Connect(h.natsURL, h.natsDialOptions()...)
	if err != nil {
		t.Fatalf("the probe connection over wss:// failed: %v", err)
	}
	if err := probe.Flush(); err != nil {
		probe.Close()
		t.Fatalf("a round trip over wss:// failed: %v", err)
	}
	probe.Close()

	line := proxy.waitForUpgradeLogLine(t)
	for _, want := range []string{
		`upgrade="websocket"`,     // the client really asked to upgrade
		":" + brokerWebSocketPort, // and nginx forwarded it to the websocket listener
		"proto=HTTP/1.1",          // over the only protocol an upgrade exists in
		"tls=TLSv1.",              // on a connection this proxy terminated
	} {
		if !strings.Contains(line, want) {
			t.Errorf("the proxy logged a 101 that does not carry %q: %s", want, line)
		}
	}

	// PROOF THREE: ask the broker who its clients are and how they
	// arrived.
	//
	// This is the same claim as proof one made from the other end, and
	// unlike proof one it speaks about the CONTROLLER's and the RUNNER's
	// own connections specifically rather than about the fixture as a
	// whole. The broker reports a connection's type directly, so this is
	// its own statement that these arrived over WebSocket rather than an
	// inference from the fixture.
	assertEveryBrokerClientArrivedThroughTheProxy(t, h, proxy)

	// NEGATIVE CONTROL ONE, and the one that makes this gate mean
	// anything at all: a raw NATS-over-TLS dial at the SAME address must
	// fail.
	//
	// nginx is an HTTP server. It answers a connection by waiting for a
	// request, while a NATS client waits for the server's INFO line, so
	// neither ever speaks and the dial times out. If this SUCCEEDED, the
	// thing in front of the broker would be forwarding bytes rather than
	// terminating HTTP, and every assertion above would be restating what
	// Toxiproxy already proves.
	//
	// Through the module's own dial path, so the bound is
	// topology.ConnectWaitTimeout rather than a number invented here.
	if nc, err := topology.Connect(ctx, "tls://"+proxy.addr, nil, "wss-gate-control",
		topology.WithTLS(h.natsCert.TLSClientConfig())); err == nil {
		nc.Close()
		t.Error("a raw tls:// NATS dial reached the broker through the proxy, so the proxy is forwarding bytes rather than terminating HTTP, and this gate proves nothing a direct listener did not already prove")
	}

	// NEGATIVE CONTROL TWO: verification is real, not skipped.
	//
	// Without it, a client configured to skip verification passes every
	// assertion above identically. It mirrors the control
	// internal/topology's transport gate already carries for tls://.
	other, err := tlscert.Generate(t.TempDir(), tlscert.Options{TTL: testsupport.ServingCertTTL})
	if err != nil {
		t.Fatalf("generating an unrelated root: %v", err)
	}
	if nc, err := topology.Connect(ctx, h.natsURL, nil, "wss-gate-control",
		topology.WithTLS(other.TLSClientConfig())); err == nil {
		nc.Close()
		t.Error("the proxy's certificate verified against a root that never signed it, so this mesh is not verifying anything")
	}
}

// withBrokerBehindTLSProxy stands a real terminating proxy in front of a
// real broker and points the whole mesh at it over wss://.
//
// It is a harness option rather than a second startHarness, which is what
// startChaosHarness had to write and what cost that file its goleak check
// and its option loop. Options run before any container, so one that
// starts its own broker can record the URL and let startHarness skip its
// own.
//
// out receives the fixture so the gate can interrogate both containers.
// A pointer parameter rather than a harness field, because nothing in the
// harness itself has any use for it.
func withBrokerBehindTLSProxy(out **proxiedBroker) harnessOption {
	return func(tb testing.TB, h *harness) {
		broker, cert := startProxiedBroker(tb)
		// wss:// rather than ws://: the proxy terminates TLS, so the hop
		// this mesh actually makes is the encrypted one.
		h.natsURL = "wss://" + broker.addr
		h.natsCert = cert
		*out = broker
	}
}

// startProxiedBroker brings up the broker and the proxy in front of it,
// and returns the address the mesh dials plus the certificate it verifies
// against.
func startProxiedBroker(tb testing.TB) (*proxiedBroker, testsupport.ServingCert) {
	tb.Helper()
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		tb.Fatalf("creating the proxied broker's network: %v", err)
	}
	tb.Cleanup(func() { _ = nw.Remove(context.Background()) })

	natsC := startBrokerWithWebSocket(tb, nw)

	// The docker daemon's host, read from the container that is already
	// up. The proxy's mapped port lands on this address, so it is what a
	// client dials and therefore what the certificate has to cover.
	// tlscert.Generate always carries localhost, 127.0.0.1 and ::1, which
	// is enough on a local daemon; naming it explicitly is what keeps this
	// honest on one that is not.
	host, err := natsC.Host(ctx)
	if err != nil {
		tb.Fatalf("reading the docker host: %v", err)
	}
	cert, err := tlscert.Generate(tb.TempDir(), tlscert.Options{
		TTL: testsupport.ServingCertTTL,
		// "nginx" so anything inside the network can reach the proxy by
		// its alias without a name mismatch.
		ExtraNames: []string{host, "nginx"},
	})
	if err != nil {
		tb.Fatalf("generating the proxy's serving certificate: %v", err)
	}

	nginxC, addr := startTerminatingProxy(tb, nw.Name, cert)
	broker := &proxiedBroker{nats: natsC, nginx: nginxC, addr: addr}
	broker.waitForTLS(tb, cert)
	return broker, cert
}

// startBrokerWithWebSocket starts the real broker with a real websocket
// block and no route to it from this host.
func startBrokerWithWebSocket(tb testing.TB, nw *testcontainers.DockerNetwork) testcontainers.Container {
	tb.Helper()

	// Exactly the block helm/the-pleiades/templates/nats-config.yaml
	// renders for nats.websocket.enabled=true with
	// nats.websocket.tls=false, which is the "TLS is terminated in front
	// of me" mode this gate exists to prove. Writing a different one here
	// would prove a configuration no chart can produce.
	conf := "websocket {\n  port: " + brokerWebSocketPort + "\n  no_tls: true\n}\n"

	// The websocket port and NOTHING ELSE. This is the single most
	// load-bearing line in this file, and the reason it must be written
	// out rather than left off lives on WithNATSExposedPorts: an empty
	// list publishes every port the IMAGE declares, which for this image
	// is 4222, 6222 and 8222.
	//
	// What naming one port leaves unpublished is 4222, the plain client
	// port, which is the route that would let a client skip the proxy
	// entirely. The gate asserts that below rather than trusting this
	// comment, and the broker's own account of its clients closes the
	// remaining gap.
	return testsupport.StartNATS(tb,
		testsupport.WithNATSConfig(conf),
		testsupport.WithNATSExposedPorts(brokerWebSocketPort),
		testsupport.WithNATSNetwork(nw, "nats"),
	).Container
}

// startTerminatingProxy starts nginx in front of the broker and returns it
// with the host-side address it publishes.
//
// It must start AFTER the broker: proxy_pass names a literal upstream, so
// nginx resolves "nats" through Docker's embedded DNS at config load and
// refuses to start with "host not found in upstream" if the alias is not
// live yet. That is the first failure anyone will see if this fixture is
// reordered.
func startTerminatingProxy(tb testing.TB, netName string, cert testsupport.ServingCert) (testcontainers.Container, string) {
	tb.Helper()
	ctx := context.Background()

	confPath := filepath.Join(tb.TempDir(), "nginx.conf")
	if err := os.WriteFile(confPath, []byte(terminatingProxyConf), 0o600); err != nil {
		tb.Fatalf("writing the proxy config: %v", err)
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: testsupport.NginxImage,
			Files: []testcontainers.ContainerFile{
				{HostFilePath: confPath, ContainerFilePath: "/etc/nginx/nginx.conf", FileMode: 0o644},
				{HostFilePath: cert.CertFile, ContainerFilePath: "/etc/nginx/tls/cert.pem", FileMode: 0o644},
				// 0600, because nginx's master process reads both files
				// as root at config load before dropping to the worker
				// user, so a private key never has to be readable by
				// anyone else to be served.
				{HostFilePath: cert.KeyFile, ContainerFilePath: "/etc/nginx/tls/key.pem", FileMode: 0o600},
			},
			ExposedPorts:   []string{"443/tcp"},
			Networks:       []string{netName},
			NetworkAliases: map[string][]string{netName: {"nginx"}},
			// Two strategies, because each alone is green for a reason
			// that is not readiness: a bound port says nothing about
			// whether the key pair loaded, and the log line says nothing
			// about whether the port is reachable from this host yet.
			//
			// NOT wait.ForHTTP, deliberately, even though it speaks TLS
			// and would be the obvious choice. It probes with an
			// http.Transport it never closes, so a successful probe
			// leaves an idle connection's read loop alive for ninety
			// seconds, and startHarness registers goleak. The real
			// handshake check is waitForTLS below, from a client this
			// file owns and closes.
			WaitingFor: wait.ForAll(
				wait.ForLog("start worker processes").WithStartupTimeout(testsupport.ContainerStartupTimeout),
				wait.ForListeningPort("443/tcp").WithStartupTimeout(testsupport.ContainerStartupTimeout),
			),
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c) // a failed start still returns its container
		tb.Fatalf("starting the terminating proxy: %v", err)
	}
	tb.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	host, err := c.Host(ctx)
	if err != nil {
		tb.Fatalf("reading the proxy's host: %v", err)
	}
	mapped, err := c.MappedPort(ctx, "443/tcp")
	if err != nil {
		tb.Fatalf("reading the proxy's mapped port: %v", err)
	}
	return c, host + ":" + mapped.Port()
}

// waitForTLS completes a real handshake against the proxy before anything
// else runs.
//
// It exists so that a certificate or key problem reports itself as "the
// proxy would not serve TLS" rather than as a dispatch that mysteriously
// never completes, which is the same failure wearing a costume that costs
// an afternoon.
func (p *proxiedBroker) waitForTLS(tb testing.TB, cert testsupport.ServingCert) {
	tb.Helper()

	transport := &http.Transport{TLSClientConfig: cert.TLSClientConfig()}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 10 * time.Second * raceTimeScale, Transport: transport}

	deadline := time.Now().Add(30 * time.Second * raceTimeScale)
	var last error
	for {
		resp, err := client.Get("https://" + p.addr + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			tb.Fatalf("the terminating proxy never answered /healthz over TLS; last error: %v", last)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForUpgradeLogLine returns the proxy's own access log line for a
// completed WebSocket upgrade.
//
// The container's log stream carries nginx's error log and access log
// merged, which is why the access format below opens with a prefix this
// can filter on.
func (p *proxiedBroker) waitForUpgradeLogLine(tb testing.TB) string {
	tb.Helper()
	ctx := context.Background()

	deadline := time.Now().Add(15 * time.Second * raceTimeScale)
	for {
		reader, err := p.nginx.Logs(ctx)
		if err == nil {
			body, readErr := io.ReadAll(reader)
			reader.Close()
			if readErr == nil {
				for _, line := range strings.Split(string(body), "\n") {
					if strings.Contains(line, proxyLogPrefix) && strings.Contains(line, "status=101") {
						return line
					}
				}
			}
		}
		if time.Now().After(deadline) {
			tb.Fatalf("the proxy never logged a 101 Switching Protocols, so nothing here proves it terminated an HTTP upgrade rather than forwarding bytes; last read error: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// brokerConnz is the part of the broker's /connz report this gate reads.
type brokerConnz struct {
	Connections []struct {
		IP   string `json:"ip"`
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"connections"`
}

// assertEveryBrokerClientArrivedThroughTheProxy reads the broker's own
// account of its clients, through the proxy, and requires every one of
// them to be a WebSocket connection from the proxy's address.
//
// "type" is the broker's own word for how a client arrived, verified
// present on the pinned nats-server rather than assumed: it reads
// "websocket" for a client that completed an upgrade. That makes this the
// only assertion in the file that is the BROKER's statement about the
// transport rather than the test's inference from the fixture.
func assertEveryBrokerClientArrivedThroughTheProxy(t *testing.T, h *harness, p *proxiedBroker) {
	t.Helper()
	ctx := context.Background()

	proxyIP, err := p.nginx.ContainerIP(ctx)
	if err != nil {
		t.Fatalf("reading the proxy's container address: %v", err)
	}

	transport := &http.Transport{TLSClientConfig: h.natsCert.TLSClientConfig()}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 15 * time.Second * raceTimeScale, Transport: transport}

	resp, err := client.Get("https://" + p.addr + "/connz")
	if err != nil {
		t.Fatalf("reading the broker's /connz through the proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the /connz body: %v", err)
	}
	var connz brokerConnz
	if err := json.Unmarshal(body, &connz); err != nil {
		t.Fatalf("decoding /connz: %v\n%s", err, body)
	}

	// A floor rather than an exact count, because an exact one is a test
	// of how many connections the two binaries happen to open. Five is
	// what they guarantee between them: the controller's bus, lock
	// manager and log streamer, and the runner's bus and dispatch.
	const guaranteedConnections = 5
	if len(connz.Connections) < guaranteedConnections {
		t.Fatalf("the broker reports %d clients, want at least %d (the controller's bus, lock manager and log streamer, and the runner's bus and dispatch). Either a binary never connected or /connz is being read wrong:\n%s",
			len(connz.Connections), guaranteedConnections, body)
	}
	for _, conn := range connz.Connections {
		if conn.IP != proxyIP {
			t.Errorf("the broker has a client %q from %s; every client must have arrived through the proxy at %s", conn.Name, conn.IP, proxyIP)
		}
		if conn.Type != "websocket" {
			t.Errorf("the broker reports client %q as type %q, want websocket: this is the broker's own statement about how the connection arrived, and anything else means the mesh did not traverse the proxy", conn.Name, conn.Type)
		}
	}
}
