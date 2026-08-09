package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/nats-io/nats.go"
	"github.com/testcontainers/testcontainers-go"
	natscontainer "github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startBenchNatsContainer boots an ephemeral NATS container with JetStream
// enabled and returns its connection URL, shared by every benchmark below.
func startBenchNatsContainer(b *testing.B) string {
	b.Helper()
	ctx := context.Background()

	natsContainer, err := natscontainer.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		b.Fatalf("failed to start nats: %v", err)
	}
	b.Cleanup(func() { natsContainer.Terminate(context.Background()) })

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		b.Fatalf("failed to get connection string: %v", err)
	}
	return url
}

// BenchmarkNatsBusPublish measures natsBus.Publish's real throughput: a
// durable, at-least-once, JetStream-backed publish, envelope stamping and
// producer-side dedup header included.
func BenchmarkNatsBusPublish(b *testing.B) {
	ctx := context.Background()
	url := startBenchNatsContainer(b)

	bus, err := event.NewNatsBus(ctx, url)
	if err != nil {
		b.Fatalf("failed to init bus: %v", err)
	}

	mockPayload := map[string]string{"metrics": "healthy"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A fresh ID per iteration: DefaultIdempotencyKeyDerivation keys
		// off Event.ID (see dedup.go), and JetStream's producer-side
		// dedup window (topology.StreamConfig's Duplicates) would
		// otherwise silently collapse every iteration after the first
		// into a no-op duplicate of the same ID, understating real,
		// distinct-message publish cost.
		evt, err := event.WrapPayload(fmt.Sprintf("bench-id-%d", i), "device.metrics", mockPayload)
		if err != nil {
			b.Fatalf("failed to wrap payload: %v", err)
		}
		if err := bus.Publish(ctx, "pleiades.events.device.metrics", *evt); err != nil {
			b.Fatalf("failed to publish: %v", err)
		}
	}
	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.Logf("[MEASURED] natsBus.Publish (JetStream, durable, at-least-once): %.0f ns/op, ~%.0f msgs/sec", nsPerOp, 1e9/nsPerOp)
}

// BenchmarkNatsCorePublish measures the honest floor natsBus.Publish is
// layered on top of: a bare core NATS publish (github.com/nats-io/nats.go's
// nats.Conn.Publish directly, no JetStream) with none of JetStream's
// durability, at-least-once redelivery, or persistence guarantees. This is
// the fair "industry alternative" comparison for this specific phase: not a
// different message broker product, but the cost this codebase's own
// durability guarantee actually pays, measured on the identical broker and
// hardware rather than an unverifiable published figure for a different
// system.
func BenchmarkNatsCorePublish(b *testing.B) {
	url := startBenchNatsContainer(b)

	nc, err := nats.Connect(url)
	if err != nil {
		b.Fatalf("failed to connect: %v", err)
	}
	b.Cleanup(nc.Close)

	mockPayload := map[string]string{"metrics": "healthy"}
	evt, err := event.WrapPayload("bench-id", "device.metrics", mockPayload)
	if err != nil {
		b.Fatalf("failed to wrap payload: %v", err)
	}
	data, err := json.Marshal(evt)
	if err != nil {
		b.Fatalf("failed to marshal: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := nc.Publish("pleiades.events.device.metrics", data); err != nil {
			b.Fatalf("failed to publish: %v", err)
		}
	}
	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.Logf("[MEASURED] bare nats.Conn.Publish (core NATS, no JetStream durability): %.0f ns/op, ~%.0f msgs/sec", nsPerOp, 1e9/nsPerOp)
}

// BenchmarkNatsBusPublishSubscribeRoundTrip measures natsBus's real
// publish-to-handler-invoked latency through a durable consumer, the same
// round-trip shape BenchmarkInProcessPublish measures for the in-process
// adapter, so the two can be read side by side.
func BenchmarkNatsBusPublishSubscribeRoundTrip(b *testing.B) {
	ctx := context.Background()
	url := startBenchNatsContainer(b)

	bus, err := event.NewNatsBus(ctx, url)
	if err != nil {
		b.Fatalf("failed to init bus: %v", err)
	}

	done := make(chan struct{})
	err = bus.Subscribe(ctx, "pleiades.events.device.roundtrip", func(evt event.Event) error {
		done <- struct{}{}
		return nil
	})
	if err != nil {
		b.Fatalf("failed to subscribe: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A fresh ID per iteration: see BenchmarkNatsBusPublish's own
		// comment on why a reused ID would silently deduplicate every
		// iteration after the first into a no-op, which for this specific
		// benchmark does not just understate throughput but hangs the
		// benchmark outright, since a deduplicated publish never triggers
		// a new delivery for <-done to receive.
		evt, err := event.WrapPayload(fmt.Sprintf("bench-roundtrip-id-%d", i), "device.roundtrip", map[string]string{"k": "v"})
		if err != nil {
			b.Fatalf("failed to wrap payload: %v", err)
		}
		if err := bus.Publish(ctx, "pleiades.events.device.roundtrip", *evt); err != nil {
			b.Fatalf("failed to publish: %v", err)
		}
		<-done
	}
	b.StopTimer()

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.Logf("[MEASURED] natsBus publish-to-handler-invoked round trip (real network hop + JetStream durability): %.0f ns/op", nsPerOp)
}
