//go:build integration

// This file severs the mesh's two network boundaries in turn and asserts
// what the platform does about it.
//
// .AGENTS/AGENTS.md's Bulletproof Testing Matrix requires Toxiproxy at
// network boundaries in their respective Release Gates, and Phase 18 is
// the first phase with two of them. Both containers sit on a shared
// Docker network behind a Toxiproxy container, and the controller and
// runner subprocesses dial the proxy's host-mapped ports, so every query
// and every publish crosses a boundary this file can cut.
//
// The two halves are not equally novel, and it is worth saying which is
// which. Severing NATS restates a claim internal/event's own chaos test
// already carries, at a higher level: it now also covers the lock
// manager, both leader electors, and the fan-out reaper living in the
// same process. Severing PostgreSQL is genuinely new coverage. Nothing
// anywhere else in this repository cuts a database connection, and the
// property it proves matters: a launch that cannot be persisted must be
// refused rather than accepted, because a caller who receives 202 is
// entitled to believe the work is recorded and will happen.
package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth/authtest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"
)

// toxiproxyImage is pinned rather than floating, for the same reason
// internal/testsupport pins every other image this suite runs.
const toxiproxyImage = testsupport.ToxiproxyImage

// chaosHarness is a harness whose database and broker can be cut.
type chaosHarness struct {
	*harness

	// postgres and nats are the control handles for the two proxies. A
	// Disable is a hard severance, not a slowdown toxic, because the
	// question here is what happens when a dependency is genuinely gone.
	postgres *toxiproxyclient.Proxy
	nats     *toxiproxyclient.Proxy
}

// startChaosHarness brings up the same mesh startHarness does, with both
// dependencies reachable only through Toxiproxy.
func startChaosHarness(tb testing.TB) *chaosHarness {
	tb.Helper()
	ctx := context.Background()

	if t, ok := tb.(*testing.T); ok {
		t.Cleanup(func() { verifyNoChaosLeaks(t) })
	}

	nw, err := network.New(ctx)
	if err != nil {
		tb.Fatalf("creating the shared network: %v", err)
	}
	tb.Cleanup(func() { _ = nw.Remove(context.Background()) })

	// Both servers get a network alias, which is the address Toxiproxy
	// uses upstream. Neither publishes a host port of its own: the only
	// way in is through the proxy.
	//
	// That claim used to be false of the broker, which declared
	// ExposedPorts 4222 out of habit. testcontainers publishes every
	// exposed port to a random host port, so there was a second route in
	// the whole time. Nothing here read it, so nothing broke, but the
	// identical claim is load bearing in the wss:// traversal gate, where
	// a second route would make every assertion pass without proving
	// anything. Container to container traffic on a user-defined network
	// needs no exposed port, so removing it costs nothing and makes the
	// sentence above true.
	pgContainer, err := testpg.Run(ctx,
		testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("pleiades"),
		testsupport.PostgresReady(),
		network.WithNetwork([]string{"postgres"}, nw),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(pgContainer) // a failed start still returns its container
		tb.Fatalf("starting the postgres container: %v", err)
	}
	tb.Cleanup(func() { _ = testcontainers.TerminateContainer(pgContainer) })

	// The alias is what tctoxiproxy.WithProxy's "nats:4222" upstream
	// below resolves through Docker's embedded DNS, so it is named here
	// rather than defaulted.
	testsupport.StartNATS(tb, testsupport.WithNATSNetwork(nw, "nats"))

	// Proxies are allocated listen ports in declaration order starting at
	// 8666, so postgres is 8666 and nats is 8667.
	toxi, err := tctoxiproxy.Run(ctx,
		toxiproxyImage,
		tctoxiproxy.WithProxy("postgres", "postgres:5432"),
		tctoxiproxy.WithProxy("nats", "nats:4222"),
		network.WithNetwork([]string{"toxiproxy"}, nw),
		testsupport.ToxiproxyReady(),
	)
	if err != nil {
		_ = testcontainers.TerminateContainer(toxi) // a failed start still returns its container
		tb.Fatalf("starting the toxiproxy container: %v", err)
	}
	tb.Cleanup(func() { _ = testcontainers.TerminateContainer(toxi) })

	pgHost, pgPort, err := toxi.ProxiedEndpoint(8666)
	if err != nil {
		tb.Fatalf("reading the proxied postgres endpoint: %v", err)
	}
	natsHost, natsPort, err := toxi.ProxiedEndpoint(8667)
	if err != nil {
		tb.Fatalf("reading the proxied nats endpoint: %v", err)
	}

	toxiURI, err := toxi.URI(ctx)
	if err != nil {
		tb.Fatalf("reading the toxiproxy control URI: %v", err)
	}
	proxies, err := toxiproxyclient.NewClient(toxiURI).Proxies()
	if err != nil {
		tb.Fatalf("listing proxies: %v", err)
	}

	ch := &chaosHarness{harness: &harness{}}
	var ok bool
	if ch.postgres, ok = proxies["postgres"]; !ok {
		tb.Fatal("toxiproxy has no \"postgres\" proxy registered")
	}
	if ch.nats, ok = proxies["nats"]; !ok {
		tb.Fatal("toxiproxy has no \"nats\" proxy registered")
	}

	// Everything downstream of here is the ordinary harness, dialing the
	// proxy instead of the server.
	ch.dsn = "postgres://pleiades:pleiades@" + pgHost + ":" + pgPort + "/pleiades?sslmode=disable"
	ch.natsURL = "nats://" + natsHost + ":" + natsPort
	ch.wire(tb)

	return ch
}

// verifyNoChaosLeaks is deliberately a no-op with a written reason.
//
// goleak cannot be used in this file. Severing a broker connection leaves
// nats.go's own reconnect machinery running by design, which is the
// behavior under test rather than a leak, and the test process holds a
// connection through a proxy that was cut and restored. Asserting zero
// goroutines here would either fail for correct behavior or force an
// ignore list broad enough to prove nothing. The leak claim for this
// harness is carried by the tests in integration_test.go, which drive the
// identical wiring without cutting anything.
func verifyNoChaosLeaks(*testing.T) {}

// TestChaos_NATSSeverance proves the mesh reports a severed broker and
// recovers without either binary restarting.
//
// What would have to break for this to fail: readiness ceasing to consult
// the broker at all (so an operator's probe stays green while nothing can
// be dispatched), or the reconnect path failing to re-establish, leaving
// a process that is up but permanently useless.
func TestChaos_NATSSeverance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the chaos test in short mode")
	}

	ch := startChaosHarness(t)
	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	token := issuer.Token(t, &auth.Identity{Subject: "e2e-chaos", Role: auth.RoleAdmin})

	// Baseline: the mesh works before anything is cut.
	ch.completeOneDispatch(t, token, "baseline")

	if err := ch.nats.Disable(); err != nil {
		t.Fatalf("disabling the nats proxy: %v", err)
	}

	// Readiness must notice. It reads the broker's own connection state,
	// so this is a real signal rather than a proxy for one, and it is
	// polled because the client takes a moment to observe the severance.
	ch.waitForHTTPStatusNot(t, "/readyz", http.StatusOK, "readiness to report the severed broker")

	if err := ch.nats.Enable(); err != nil {
		t.Fatalf("re-enabling the nats proxy: %v", err)
	}

	// Recovery, proven by real work rather than by a green probe: a
	// brand new dispatch must complete end to end, with neither the
	// controller nor the runner having been restarted.
	ch.waitForHTTPStatus(t, "/readyz", http.StatusOK, "readiness to recover after the broker returned")
	ch.completeOneDispatch(t, token, "after the broker returned")
}

// TestChaos_PostgresSeverance proves a launch that cannot be persisted is
// refused rather than accepted, and that the mesh recovers.
//
// This is the half with no owner anywhere else in the repository. What
// would have to break for it to fail: the dispatch handler answering 202
// on a failed write, which would hand a caller a job identifier for work
// that was never recorded and will never run. That is the worst failure
// shape an accept-then-queue API has, because nothing downstream ever
// reports it.
func TestChaos_PostgresSeverance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the chaos test in short mode")
	}

	ch := startChaosHarness(t)
	issuer := authtest.NewWithSecret(t, harnessJWTSecret, harnessJWTIssuer, harnessJWTAudience)
	token := issuer.Token(t, &auth.Identity{Subject: "e2e-chaos", Role: auth.RoleAdmin})

	ch.completeOneDispatch(t, token, "baseline")

	if err := ch.postgres.Disable(); err != nil {
		t.Fatalf("disabling the postgres proxy: %v", err)
	}

	// The readiness probe issues a real query, which is exactly why it
	// exists: pinging a connection pool would not have noticed.
	ch.waitForHTTPStatusNot(t, "/readyz", http.StatusOK, "readiness to report the severed database")

	// The load-bearing assertion. A launch must not be accepted when the
	// job row cannot be written.
	status, body := ch.launch(t, token)
	if status == http.StatusAccepted {
		t.Fatalf("a dispatch was accepted with the database severed, so a caller holds a job id for work that was never recorded. Body: %s", body)
	}
	if status < 500 || status > 599 {
		t.Fatalf("a dispatch with the database severed returned %d; want a 5xx, since the failure is the server's and a retry is the right response. Body: %s", status, body)
	}

	if err := ch.postgres.Enable(); err != nil {
		t.Fatalf("re-enabling the postgres proxy: %v", err)
	}

	ch.waitForHTTPStatus(t, "/readyz", http.StatusOK, "readiness to recover after the database returned")
	ch.completeOneDispatch(t, token, "after the database returned")
}

// waitForHTTPStatusNot polls path until it answers with anything other
// than unwanted.
//
// It is the inverse of waitForHTTPStatus and exists for the same reason:
// a severance takes a moment to be observed, so asserting the degraded
// state with a single immediate request would race the client's own
// detection and fail for the wrong reason.
func (h *harness) waitForHTTPStatusNot(tb testing.TB, path string, unwanted int, what string) {
	tb.Helper()

	deadline := time.Now().Add(60 * time.Second * raceTimeScale)
	last := unwanted

	client := h.httpClient()
	for time.Now().Before(deadline) {
		resp, err := client.Get(h.baseURL + path)
		if err != nil {
			// A refused or torn connection is itself "not the wanted
			// status", and is a legitimate way for a severed dependency
			// to surface.
			return
		}
		last = resp.StatusCode
		resp.Body.Close()
		if resp.StatusCode != unwanted {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	tb.Fatalf("timed out waiting for %s (%s still returns %d)\n%s", what, path, last, h.controller.output())
}
