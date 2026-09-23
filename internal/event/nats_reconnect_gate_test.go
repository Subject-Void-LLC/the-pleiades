package event_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go/jetstream"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"
)

// This file is Phase 96a's Release Gate. Both tests are throwaway
// diagnostics promoted into permanent gates, each INVERTED to the
// post-fix expectation and each citing the pre-fix measurement it
// replaces, so the gate is falsifiable in both directions: it fails if
// the fix regresses, and it would have failed before the fix landed.
//
// Why the existing TestNatsBus_SurvivesConnectionSeverance is not this
// gate: it severs for 5 seconds and polls 20 for recovery, and the
// defect appears at 2m3s. That test passed for the whole time the bug
// existed, because it never reached it. A regression guard has to cross
// the boundary it is guarding.
//
// goleak is deliberately absent here, and that is a decision rather than
// an oversight. tests/e2e/integration_chaos_test.go already records
// verifyNoChaosLeaks as an intentional no-op because severing a broker
// leaves nats.go's own reconnect machinery running by design, which is
// the behaviour under test rather than a leak. MaxReconnects(-1) makes
// that strictly more true: the reconnect loop is now unbounded on
// purpose, so a leak checker would report the feature.

// natsThroughToxiproxy starts a real NATS container and a real Toxiproxy
// in front of it, returning the proxied URL and the live proxy handle the
// caller severs with.
//
// The harness is extracted from nats_chaos_test.go, which had it inline
// as the only user. Both files sever the same boundary, and a second
// hand-rolled copy of a container topology is exactly the drift this
// repository keeps finding.
// It takes testing.TB rather than *testing.T so the recovery benchmark
// can stand up the identical topology instead of hand-rolling a second
// copy of it. Only Helper, Fatalf and Cleanup are used, all of which are
// on TB with identical semantics for both.
func natsThroughToxiproxy(t testing.TB) (string, *toxiproxyclient.Proxy) {
	t.Helper()
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("failed to create network: %v", err)
	}
	t.Cleanup(func() { nw.Remove(context.Background()) })

	// The alias is what the proxy's upstream resolves, so it is passed
	// explicitly rather than defaulted: "nats:4222" below is this line.
	testsupport.StartNATS(t, testsupport.WithNATSNetwork(nw, "nats"))

	toxiproxyContainer, err := tctoxiproxy.Run(ctx,
		testsupport.ToxiproxyImage,
		tctoxiproxy.WithProxy("nats", "nats:4222"),
		network.WithNetwork([]string{"toxiproxy"}, nw),
	)
	if err != nil {
		t.Fatalf("failed to start toxiproxy container: %v", err)
	}
	t.Cleanup(func() { toxiproxyContainer.Terminate(context.Background()) })

	proxiedHost, proxiedPort, err := toxiproxyContainer.ProxiedEndpoint(8666)
	if err != nil {
		t.Fatalf("failed to get proxied endpoint: %v", err)
	}
	toxiURI, err := toxiproxyContainer.URI(ctx)
	if err != nil {
		t.Fatalf("failed to get toxiproxy control URI: %v", err)
	}
	proxies, err := toxiproxyclient.NewClient(toxiURI).Proxies()
	if err != nil {
		t.Fatalf("failed to list proxies: %v", err)
	}
	proxy, ok := proxies["nats"]
	if !ok {
		t.Fatal("toxiproxy has no \"nats\" proxy registered")
	}
	return "nats://" + proxiedHost + ":" + proxiedPort, proxy
}

// severanceBeyondTheOldBudget is how long D1 cuts the link for.
//
// The number is derived rather than picked: the pre-fix client gave up at
// a measured 2m3s (60 reconnect attempts, 2s apart, plus dial timeouts
// and jitter), so anything at or below that would pass against the old
// code and prove nothing. Two and a half minutes clears it with margin
// while keeping the test's cost bounded.
const severanceBeyondTheOldBudget = 150 * time.Second

// TestNatsBus_RecoversFromAnOutageBeyondTheOldReconnectBudget is D1,
// inverted.
//
// PRE-FIX MEASUREMENT (2026-08-22, real NATS behind real Toxiproxy, bare
// nats.Connect): ClosedHandler fired 2m3s after the cut, IsClosed() was
// true, disconnect callbacks 2 and reconnect callbacks ZERO. The network
// was then fully healed and watched 30 more seconds: no recovery, status
// CLOSED, and a publish returning "nats: connection closed". A Runner
// that lost its link for two minutes was dead until a human restarted the
// process.
//
// POST-FIX EXPECTATION, asserted below: the same client, severed for
// longer than that budget, publishes successfully once the link heals,
// without anything reconnecting it explicitly.
func TestNatsBus_RecoversFromAnOutageBeyondTheOldReconnectBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	url, proxy := natsThroughToxiproxy(t)
	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init nats bus through proxy: %v", err)
	}
	t.Cleanup(func() { bus.Close() })

	const topic = "pleiades.events.chaos.longoutage"
	if err := bus.Publish(ctx, topic, event.Event{ID: "before-outage"}); err != nil {
		t.Fatalf("baseline publish failed, so nothing below would mean anything: %v", err)
	}

	// Disable, not a slowdown toxic: a hard TCP cut that closes the
	// existing connection and refuses new ones, which is what a satellite
	// pass gap or a jam window looks like from the client side.
	if err := proxy.Disable(); err != nil {
		t.Fatalf("failed to sever the connection: %v", err)
	}
	time.Sleep(severanceBeyondTheOldBudget)

	if err := proxy.Enable(); err != nil {
		t.Fatalf("failed to heal the connection: %v", err)
	}

	// The client reconnects on its own schedule, so poll rather than
	// assuming the first publish after the heal lands. What is being
	// asserted is that recovery happens at all: pre-fix this loop would
	// have run to its deadline returning "nats: connection closed" every
	// time, because the connection was CLOSED and stayed that way.
	deadline := time.Now().Add(60 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		lastErr = bus.Publish(publishCtx, topic, event.Event{ID: "after-long-outage"})
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}

	if lastErr != nil && strings.Contains(lastErr.Error(), "connection closed") {
		t.Fatalf("the connection was CLOSED and never came back after a %v outage, which is exactly the pre-fix D1 behaviour this gate exists to prevent: %v", severanceBeyondTheOldBudget, lastErr)
	}
	t.Fatalf("no publish succeeded within 60s of healing a %v outage: %v", severanceBeyondTheOldBudget, lastErr)
}

// TestNatsBus_ConnectsWhenTheBrokerAppearsAfterStartup is D2, inverted.
//
// PRE-FIX MEASUREMENT (2026-08-22): a dial before the broker existed
// failed two different ways, and the prediction that it would surface as
// ErrNoServers was wrong in both. Against a listener whose backend was
// down (a proxy in front of a not-yet-started broker, the realistic
// deployment shape and the one reproduced here) it failed in 2ms with a
// bare EOF. Against a black-holed address it failed in exactly 2s with
// "dial tcp: i/o timeout". A fresh call once the broker became reachable
// succeeded in 11ms, so recovery was always possible and simply never
// attempted: nothing at the call site retried, and cmd/runner called
// log.Fatalf.
//
// POST-FIX EXPECTATION, asserted below: constructing the bus against a
// severed proxy blocks rather than failing instantly, and once the
// backend appears the same construction completes and the bus works.
func TestNatsBus_ConnectsWhenTheBrokerAppearsAfterStartup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	url, proxy := natsThroughToxiproxy(t)

	// Sever before constructing anything: from the client's point of view
	// this is a broker that has not started yet, which is the cold-start
	// case every deployment has and nothing orders.
	if err := proxy.Disable(); err != nil {
		t.Fatalf("failed to sever the connection: %v", err)
	}

	type result struct {
		bus event.Bus
		err error
	}
	done := make(chan result, 1)
	go func() {
		bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
		done <- result{bus: bus, err: err}
	}()

	// Pre-fix, this construction returned within 2ms. If it returns
	// before the broker is reachable, the retry behaviour is gone.
	select {
	case r := <-done:
		if r.bus != nil {
			r.bus.Close()
		}
		t.Fatalf("NewNatsBus returned before the broker was reachable (err=%v), which is the pre-fix D2 behaviour: RetryOnFailedConnect should have kept it waiting", r.err)
	case <-time.After(2 * time.Second):
	}

	if err := proxy.Enable(); err != nil {
		t.Fatalf("failed to restore the connection: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("NewNatsBus failed even though the broker became reachable: %v", r.err)
		}
		t.Cleanup(func() { r.bus.Close() })

		publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := r.bus.Publish(publishCtx, "pleiades.events.chaos.coldstart", event.Event{ID: "after-cold-start"}); err != nil {
			t.Fatalf("the bus connected but could not publish, so the stream was never ensured: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("NewNatsBus never returned after the broker became reachable")
	}
}

// TestNatsBus_ColdStartStillFailsWhenTheBrokerNeverAppears is the control
// for the test above, and it is the reason that one is not vacuous.
//
// Waiting forever would also pass D2's assertion while making every
// composition root hang at startup against a typo in NATS_URL. The bound
// is topology.ConnectWaitTimeout, and this proves the constructor honours
// it and reports a usable error rather than blocking indefinitely.
func TestNatsBus_ColdStartStillFailsWhenTheBrokerNeverAppears(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// RFC 5737 TEST-NET-1: guaranteed unroutable, no container needed.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	bus, err := event.NewNatsBus(ctx, "nats://192.0.2.1:4222", nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	elapsed := time.Since(start)

	if err == nil {
		bus.Close()
		t.Fatal("NewNatsBus succeeded against an unroutable address")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("NewNatsBus failed for the wrong reason: %v", err)
	}
	if elapsed > 30*time.Second {
		t.Errorf("NewNatsBus took %v to give up on an unroutable broker; the wait is meant to be bounded so a typo in NATS_URL is a startup error rather than a hang", elapsed)
	}
}

// TestNatsBus_LogsTheConnectionLifecycle is the Observer half of this
// phase's Release Gate, and it exists because its absence let two real
// defects through review.
//
// The phase's third measured finding was that a disconnect was completely
// silent: no dial site set any handler, so a Runner could stop working
// without emitting a line. The first attempt at proving that fixed
// asserted only that the callback fields were non-nil, which is a
// tautology about a struct, not evidence about behaviour. Under that
// assertion two defects passed: the disconnect line reported url="" on
// every disconnect (nats.go has already left CONNECTED by the time the
// handler runs), and a graceful Close fired both the disconnect and the
// closed handler, so every deliberate shutdown logged a WARN saying
// "reconnecting" and an ERROR saying "closed permanently" about a
// shutdown going exactly to plan.
//
// This captures what a real logger actually receives across a real
// severance and a real graceful close.
func TestNatsBus_LogsTheConnectionLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	var mu sync.Mutex
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&safeWriter{mu: &mu, w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	captured := func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}

	url, proxy := natsThroughToxiproxy(t)
	bus, err := event.NewNatsBus(ctx, url, logger, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init nats bus through proxy: %v", err)
	}

	if err := proxy.Disable(); err != nil {
		t.Fatalf("failed to sever the connection: %v", err)
	}
	waitForLog(t, captured, "nats connection lost")
	if err := proxy.Enable(); err != nil {
		t.Fatalf("failed to heal the connection: %v", err)
	}
	waitForLog(t, captured, "nats connection reestablished")

	// The reconnect line must carry a real address. The disconnect line
	// deliberately carries none, because nats.go reports an empty one
	// there and url="" reads as a connection that never had an address.
	if !strings.Contains(captured(), "url=nats://") {
		t.Errorf("no log line carried a real broker URL:\n%s", captured())
	}

	// A graceful shutdown must be quiet. Everything logged from here on
	// is what an operator sees on every rolling update.
	mu.Lock()
	buf.Reset()
	mu.Unlock()
	bus.Close()
	time.Sleep(500 * time.Millisecond)

	if after := captured(); strings.Contains(after, "reconnecting") || strings.Contains(after, "closed permanently") {
		t.Errorf("a graceful Close emitted failure logs; a Runner and a Controller hold three connections each, so this is six false lines per pod on every rolling update:\n%s", after)
	}
}

// waitForLog blocks until captured() contains want, or fails.
func waitForLog(t *testing.T, captured func() string, want string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(captured(), want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no log line containing %q within 90s; before Phase 96a the connection lifecycle produced no output at all:\n%s", want, captured())
}

// safeWriter serializes writes into a buffer that the nats.go callback
// goroutine and the test goroutine both touch.
type safeWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (s *safeWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// TestNatsBus_WarnsWhenTheLiveStreamShapeDiffers is Phase 96b's
// observability assertion, and it is the half of that phase that
// ownership cannot deliver.
//
// Making the Controller the only process that may reshape the stream
// stops an old build reverting an operator's choice. It does nothing
// about the window during a rolling upgrade when that old build is
// running against a shape it does not expect: it is no longer doing
// damage, but it is also not applying the new configuration, and without
// this warning nothing in the system would say so.
func TestNatsBus_WarnsWhenTheLiveStreamShapeDiffers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()
	url, _ := natsThroughToxiproxy(t)

	// A Controller provisions, then an operator widens retention.
	provisioner, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("provisioning bus: %v", err)
	}
	provisioner.Close()

	nc, err := topology.Connect(ctx, url, nil, "test")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	widened := topology.StreamConfig(topology.DefaultOutageBudget)
	widened.MaxAge = 30 * 24 * time.Hour
	if _, err := js.UpdateStream(ctx, widened); err != nil {
		t.Fatalf("widening retention: %v", err)
	}

	// A Runner starts against it.
	var mu sync.Mutex
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&safeWriter{mu: &mu, w: &buf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	reader, err := event.NewNatsBus(ctx, url, logger, topology.StreamReader, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("reader bus: %v", err)
	}
	defer reader.Close()

	mu.Lock()
	logged := buf.String()
	mu.Unlock()

	if !strings.Contains(logged, "differs from what this build declares") {
		t.Errorf("no drift warning was logged against a stream with a different MaxAge:\n%s", logged)
	}
	if !strings.Contains(logged, "MaxAge") {
		t.Errorf("the drift warning did not name the field that differs:\n%s", logged)
	}

	// And the reader must not have corrected it.
	live, err := js.Stream(ctx, topology.StreamName)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if got := live.CachedInfo().Config.MaxAge; got != widened.MaxAge {
		t.Fatalf("the reader reverted MaxAge to %v, want the operator's %v", got, widened.MaxAge)
	}
}
