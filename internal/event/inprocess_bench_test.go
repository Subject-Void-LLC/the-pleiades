package event_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

// BenchmarkInProcessPublish measures the publish-to-handler-invoked round
// trip latency of InProcessBus: the time from calling Publish until the
// subscriber's handler goroutine actually runs. A channel signal from the
// handler is drained once per iteration so b.N counts full round trips, not
// just the (near-instant) return of Publish itself.
//
// nats_bench_test.go's BenchmarkNatsBusPublishSubscribeRoundTrip measures
// the identical round-trip shape for the real NATS adapter, so the two
// numbers are directly comparable: this one is expected to be orders of
// magnitude faster, having no network hop and no JetStream durability to
// pay for.
func BenchmarkInProcessPublish(b *testing.B) {
	bus := event.NewInProcessBus()

	// Unbuffered: the handler goroutine blocks on send until the benchmark
	// loop receives, so each receive corresponds to exactly one Publish
	// call's delivery completing.
	done := make(chan struct{})
	err := bus.Subscribe(context.Background(), "pleiades.events.device.metrics", func(evt event.Event) error {
		done <- struct{}{}
		return nil
	})
	if err != nil {
		b.Fatalf("failed to subscribe: %v", err)
	}

	mockPayload := map[string]string{"metrics": "healthy"}
	evt, err := event.WrapPayload("bench-id", "device.metrics", mockPayload)
	if err != nil {
		b.Fatalf("failed to wrap payload: %v", err)
	}

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := bus.Publish(ctx, "pleiades.events.device.metrics", *evt); err != nil {
			b.Fatalf("failed to publish: %v", err)
		}
		// Drain the handler's signal so this iteration's timing includes
		// the full round trip, not just Publish's own quick return.
		<-done
	}

	b.StopTimer()
	b.Logf("[MEASURED] in-process bus publish-to-handler-invoked round trip (no network hop, no JetStream durability): %.0f ns/op", float64(b.Elapsed().Nanoseconds())/float64(b.N))
}
