package event_test

import (
	"context"
	"testing"
	"time"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestNatsBus_SurvivesConnectionSeverance is this phase's Chaos Testing
// proof: AGENTS.md's Bulletproof Testing Matrix requires Toxiproxy for
// network boundaries (DB, NATS) in their respective Release Gates, to
// simulate TCP severing and validate backoff loops. natsBus is the first
// real NATS boundary in this repository to close a Release Gate, so this
// is that proof's first appearance, not a retrofit.
//
// natsBus connects through a Toxiproxy proxy fronting the real NATS
// container, not directly to it. The test proves the baseline works, then
// disables the proxy (a hard TCP severance, not a mere slowdown), proves a
// publish genuinely fails while severed rather than hanging forever or
// silently succeeding, then re-enables the proxy and proves the
// underlying nats.go client's own reconnect-with-backoff logic recovers
// without natsBus itself needing to reconnect explicitly: Publish
// succeeds again on its own once the network heals.
func TestNatsBus_SurvivesConnectionSeverance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("failed to create network: %v", err)
	}
	t.Cleanup(func() { nw.Remove(context.Background()) })

	natsContainer, err := tcnats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
		network.WithNetwork([]string{"nats"}, nw),
	)
	if err != nil {
		t.Fatalf("failed to start nats container: %v", err)
	}
	t.Cleanup(func() { natsContainer.Terminate(context.Background()) })

	// The proxy's upstream is "nats:4222" (the container's network alias
	// and NATS's default client port), reachable from the toxiproxy
	// container because both share nw.
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
	toxiClient := toxiproxyclient.NewClient(toxiURI)
	proxies, err := toxiClient.Proxies()
	if err != nil {
		t.Fatalf("failed to list proxies: %v", err)
	}
	proxy, ok := proxies["nats"]
	if !ok {
		t.Fatal("toxiproxy has no \"nats\" proxy registered")
	}

	// natsBus connects THROUGH the proxy, not directly to the NATS
	// container: every publish and subscribe below crosses the boundary
	// this test severs.
	bus, err := event.NewNatsBus(ctx, "nats://"+proxiedHost+":"+proxiedPort, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		t.Fatalf("failed to init nats bus through proxy: %v", err)
	}

	const topic = "pleiades.events.chaos.severance"
	received := make(chan event.Event, 4)
	if err := bus.Subscribe(ctx, topic, func(e event.Event) error {
		received <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// 1. Baseline: publish succeeds through the healthy proxy.
	if err := bus.Publish(ctx, topic, event.Event{ID: "before-severance"}); err != nil {
		t.Fatalf("baseline publish through proxy failed: %v", err)
	}
	select {
	case got := <-received:
		if got.ID != "before-severance" {
			t.Errorf("baseline: got ID %q, want before-severance", got.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for baseline delivery")
	}

	// 2. Sever the connection: Disable, not a slowdown toxic, is a hard
	// TCP cut, closing the existing connection and refusing new ones.
	if err := proxy.Disable(); err != nil {
		t.Fatalf("failed to disable proxy: %v", err)
	}

	// A publish attempted while severed must genuinely fail from the
	// caller's own perspective (bounded by a short timeout), not hang
	// forever and not report success back to the caller. This does NOT
	// mean the underlying bytes are necessarily lost: nats.go buffers
	// writes made while disconnected (ReconnectBufSize) and flushes them
	// once reconnected, so this same "during-severance" message may still
	// arrive later, after Enable below -- proven, not assumed, by the
	// drain loop after recovery, which tolerates seeing it interleaved
	// with "after-recovery" rather than asserting it never arrives.
	severedCtx, severedCancel := context.WithTimeout(ctx, 5*time.Second)
	defer severedCancel()
	err = bus.Publish(severedCtx, topic, event.Event{ID: "during-severance"})
	if err == nil {
		t.Error("expected publish to fail while the connection is severed, but it succeeded")
	} else {
		t.Logf("publish correctly failed while severed: %v", err)
	}

	// 3. Heal the network: nats.go's own client reconnects with backoff
	// on its own (event.NewNatsBus does not disable reconnection, nor
	// does it need any explicit reconnect call here); this test only
	// needs to wait for that to happen and prove Publish works again.
	if err := proxy.Enable(); err != nil {
		t.Fatalf("failed to re-enable proxy: %v", err)
	}

	// Retry loop, not a single attempt on a timer: nats.go's default
	// reconnect backoff is jittered, so the exact moment the client
	// becomes usable again is not deterministic; the retry loop is the
	// portable way to prove "eventually recovers" without hardcoding
	// nats.go's own internal timing as this test's own.
	deadline := time.Now().Add(20 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := bus.Publish(ctx, topic, event.Event{ID: "after-recovery"}); err == nil {
			lastErr = nil
			break
		} else {
			lastErr = err
			time.Sleep(250 * time.Millisecond)
		}
	}
	if lastErr != nil {
		t.Fatalf("publish never recovered after the network healed: %v", lastErr)
	}

	// Drain until "after-recovery" is seen, tolerating a stray
	// "during-severance" delivery arriving first if nats.go's own
	// reconnect buffer flushed it late (see the comment above).
	drainDeadline := time.After(5 * time.Second)
	found := false
	for !found {
		select {
		case got := <-received:
			if got.ID == "after-recovery" {
				found = true
				break
			}
			if got.ID != "during-severance" {
				t.Errorf("unexpected delivery ID %q", got.ID)
			}
		case <-drainDeadline:
			t.Fatal("timed out waiting for post-recovery delivery")
		}
	}
}
