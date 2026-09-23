package netconf_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/testcontainers/testcontainers-go"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestChaos_SeveringTheConnectionMidSession is this phase's Toxiproxy
// chaos coverage, required by AGENTS.md's Bulletproof Testing Matrix for
// any network boundary. It reaches a real Netopeer2 NETCONF server
// through a Toxiproxy proxy, severs the TCP connection outright, and
// asserts what happens next.
//
// The importing of internal/testsupport from a pkg/ test follows
// pkg/awscloud/client_test.go's existing precedent, and exists so the
// Toxiproxy image version is pinned in exactly one place rather than
// drifting between two.
//
// # What this asserts, and the one thing it deliberately does not
//
// A NETCONF session is NOT resynchronizable after a severed read: RFC
// 6242 framing gives no way to find where the next message begins in a
// stream that was cut mid-message, so a client that tried would be
// guessing. pkg/netconf therefore fails the session permanently, the
// same clean-immediate-failure choice remoteexec.Shell documents. So
// this test asserts three things:
//
//  1. A severed operation returns a REAL error, promptly. Not a hang,
//     which is what an unbounded read gives, and not a short message
//     handed upward as if it were complete, which is what a framing
//     decoder that trusted a truncated chunk would give. The second is
//     the dangerous one: a truncated get-config that parsed would be a
//     partial device configuration presented as a whole one.
//  2. The failed session stays failed rather than appearing to recover.
//  3. Reconnecting once the boundary heals works, through
//     pkg/remoteexec's shared dial path (pkg/retry.Do plus the per-target
//     circuit breaker) rather than through any reconnect loop of this
//     package's own. That division is the point: there is exactly one
//     retry loop in this codebase and internal/archtest's
//     TestNoSecondRetryLoopOrCircuitBreaker is what keeps it that way.
func TestChaos_SeveringTheConnectionMidSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the NETCONF chaos test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	nw, err := network.New(ctx)
	if err != nil {
		t.Skipf("could not create a docker network (is Docker running?): %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	server, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        notconfImage,
			ExposedPorts: []string{"830/tcp"},
			WaitingFor: wait.ForLog("Listening on :::830 for SSH connections").
				WithStartupTimeout(2 * time.Minute),
			Networks:       []string{nw.Name},
			NetworkAliases: map[string][]string{nw.Name: {"netconfserver"}},
		},
		Started: true,
	})
	if err != nil {
		t.Skipf("could not start the NETCONF container: %v", err)
	}
	t.Cleanup(func() { _ = server.Terminate(context.Background()) })

	// The proxy's upstream is the server's network alias, reachable from
	// the Toxiproxy container because both share nw.
	proxyContainer, err := tctoxiproxy.Run(ctx,
		testsupport.ToxiproxyImage,
		tctoxiproxy.WithProxy("netconf", "netconfserver:830"),
		network.WithNetwork([]string{"toxiproxy"}, nw),
		testsupport.ToxiproxyReady(),
	)
	if err != nil {
		t.Skipf("could not start the toxiproxy container: %v", err)
	}
	t.Cleanup(func() { _ = proxyContainer.Terminate(context.Background()) })

	proxiedHost, proxiedPort, err := proxyContainer.ProxiedEndpoint(8666)
	if err != nil {
		t.Fatalf("failed to get the proxied endpoint: %v", err)
	}
	toxiURI, err := proxyContainer.URI(ctx)
	if err != nil {
		t.Fatalf("failed to get the toxiproxy control URI: %v", err)
	}
	proxies, err := toxiproxyclient.NewClient(toxiURI).Proxies()
	if err != nil {
		t.Fatalf("failed to list proxies: %v", err)
	}
	proxy, ok := proxies["netconf"]
	if !ok {
		t.Fatal("toxiproxy has no \"netconf\" proxy registered")
	}

	// Everything below crosses the boundary this test severs.
	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	target := remoteexec.Target{Host: proxiedHost, Port: atoiOrFail(t, proxiedPort)}
	auth := remoteexec.PasswordAuth(notconfUser, notconfPassword)

	openThroughProxy := func(ctx context.Context) (*netconf.Session, func()) {
		t.Helper()
		conn, err := runner.Connect(ctx, nil, target, auth)
		if err != nil {
			t.Fatalf("connecting through the proxy: %v", err)
		}
		sub, err := conn.Subsystem(ctx, "netconf")
		if err != nil {
			_ = conn.Close()
			t.Fatalf("opening the netconf subsystem through the proxy: %v", err)
		}
		s, err := netconf.Open(ctx, sub, netconf.Options{})
		if err != nil {
			_ = sub.Close()
			_ = conn.Close()
			t.Fatalf("opening the session through the proxy: %v", err)
		}
		return s, func() { _ = sub.Close(); _ = conn.Close() }
	}

	// 1. Baseline through a healthy proxy.
	session, closeSession := openThroughProxy(ctx)
	baseline, err := session.GetConfig(ctx, datastore.Path{})
	if err != nil {
		closeSession()
		t.Fatalf("baseline GetConfig through the proxy failed: %v", err)
	}
	if len(baseline.Bytes) == 0 {
		closeSession()
		t.Fatal("baseline GetConfig returned an empty document")
	}

	// 2. Sever. Disable is a hard TCP cut: it closes the existing
	// connection and refuses new ones, rather than merely slowing them.
	if err := proxy.Disable(); err != nil {
		closeSession()
		t.Fatalf("failed to disable the proxy: %v", err)
	}

	const severedDeadline = 20 * time.Second
	severedCtx, severedCancel := context.WithTimeout(ctx, severedDeadline)
	started := time.Now()
	got, err := session.GetConfig(severedCtx, datastore.Path{})
	elapsed := time.Since(started)
	severedCancel()
	closeSession()

	if err == nil {
		t.Fatalf("GetConfig() returned %d bytes and a nil error across a severed connection: a truncated reply accepted as complete is a partial device configuration presented as a whole one", len(got.Bytes))
	}
	if len(got.Bytes) != 0 {
		t.Errorf("GetConfig() returned %d bytes alongside its error; a partial message must never be usable", len(got.Bytes))
	}
	// The distinction being drawn here is between DETECTING a broken
	// connection and merely timing out on one. A client that hung until
	// its own deadline would satisfy the error check above while still
	// pinning a task, and its device lock, for the whole deadline.
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the severed GetConfig ended by way of its own deadline (%v) rather than by reporting the broken connection: that is a hang with a timeout on it, not a detected failure", err)
	}
	if elapsed > severedDeadline/2 {
		t.Errorf("the severed GetConfig took %v of a %v deadline to fail; a severed TCP connection should surface promptly", elapsed, severedDeadline)
	}
	t.Logf("severed GetConfig failed in %v with: %v", elapsed, err)

	// 3. The failed session stays failed. Nothing here attempts
	// resynchronization, and a session that appeared to recover would be
	// reading from an unknown offset in the stream.
	if _, err := session.GetConfig(ctx, datastore.Path{}); err == nil {
		t.Error("GetConfig() succeeded on a session whose connection had been severed; this session is not resynchronizable and must stay failed")
	}

	// 4. Recovery is reconnection, through pkg/remoteexec's shared dial
	// path. This is what proves the severance was the network's and not
	// this client corrupting its own state.
	if err := proxy.Enable(); err != nil {
		t.Fatalf("failed to re-enable the proxy: %v", err)
	}
	recovered, closeRecovered := openThroughProxy(ctx)
	defer closeRecovered()

	after, err := recovered.GetConfig(ctx, datastore.Path{})
	if err != nil {
		t.Fatalf("GetConfig() after recovery failed: %v", err)
	}
	if len(after.Bytes) != len(baseline.Bytes) {
		t.Errorf("after recovery GetConfig returned %d bytes, want the same %d the baseline did: nothing changed the datastore in between",
			len(after.Bytes), len(baseline.Bytes))
	}
	if !strings.Contains(string(after.Bytes), "netconf-server") {
		t.Errorf("after recovery GetConfig returned %q, want the server's own configuration", after.Bytes)
	}
}

// atoiOrFail parses a decimal port, failing the test rather than
// returning an error nobody would check.
func atoiOrFail(t *testing.T, s string) int {
	t.Helper()
	n := 0
	if s == "" {
		t.Fatalf("empty port")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("port %q is not a decimal number", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
