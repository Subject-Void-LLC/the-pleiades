package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// BenchmarkNatsBus_RecoveryAfterSeverance is Phase 96a's Stress and
// Benchmark proof, required by AGENTS.md before every Release Gate.
//
// It measures the number an operator actually experiences, which is not
// publish throughput: how long after the link comes back does the mesh
// carry work again. Before this phase that number did not exist for any
// outage past 2m3s, because the answer was "never, until someone
// restarts the process". Now it is a latency, and a latency can regress
// quietly, which is what a benchmark is for.
//
// The severance here is deliberately SHORT (two seconds) even though the
// release gate's is 150. This measures the recovery path, and recovery
// after a long outage is the same code reached at a later point in the
// same backoff curve; paying 150 seconds per iteration would make the
// benchmark unrunnable without measuring anything the gate does not
// already assert. What this catches is a regression in how fast the
// backoff notices the link is back, which is exactly the thing
// CustomReconnectDelay could break silently.
//
// Comparison against the alternative, per AGENTS.md's requirement to
// compare against industry alternatives rather than report a bare number:
// nats.go's stock configuration reconnects on a FIXED 2s ReconnectWait
// with 100ms of jitter, so its floor for noticing a healed link is
// roughly two seconds regardless of how briefly the link was gone. The
// shared options replace that with pkg/retry.Backoff from a 250ms base,
// so an early attempt costs a fraction of the stock delay. That
// advantage inverts by design at the long end, where the backoff has
// climbed to its 30s ceiling and stock nats.go would still be retrying
// every 2s, having already given up permanently 60 attempts earlier.
func BenchmarkNatsBus_RecoveryAfterSeverance(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping container benchmark in short mode")
	}
	ctx := context.Background()

	url, proxy := natsThroughToxiproxy(b)
	bus, err := event.NewNatsBus(ctx, url, nil)
	if err != nil {
		b.Fatalf("failed to init nats bus through proxy: %v", err)
	}
	b.Cleanup(func() { bus.Close() })

	const topic = "pleiades.events.bench.recovery"
	if err := bus.Publish(ctx, topic, event.Event{ID: "baseline"}); err != nil {
		b.Fatalf("baseline publish failed: %v", err)
	}

	for b.Loop() {
		b.StopTimer()
		if err := proxy.Disable(); err != nil {
			b.Fatalf("failed to sever: %v", err)
		}
		time.Sleep(2 * time.Second)
		if err := proxy.Enable(); err != nil {
			b.Fatalf("failed to heal: %v", err)
		}
		b.StartTimer()

		// The measured window: from a healed link to a publish landing.
		deadline := time.Now().Add(60 * time.Second)
		var lastErr error
		for time.Now().Before(deadline) {
			publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			lastErr = bus.Publish(publishCtx, topic, event.Event{ID: "after-heal"})
			cancel()
			if lastErr == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if lastErr != nil {
			b.Fatalf("never recovered within 60s of healing: %v", lastErr)
		}
	}
}
