package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// TestNatsBusPublish_ProducerSideDedupSuppressesRetryOfSameEvent proves
// natsBus.Publish's use of jetstream.WithMsgID engages JetStream's own
// producer-side dedup window (PLAN.md Section 26.2's "producer half"): two
// Publish calls with the same already-built Event (the shape a caller-side
// retry actually takes) result in exactly one message stored on the
// stream, not two.
func TestNatsBusPublish_ProducerSideDedupSuppressesRetryOfSameEvent(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url)
	if err != nil {
		t.Fatalf("failed to init bus: %v", err)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	stream, err := js.Stream(ctx, topology.StreamName)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	before, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("info before: %v", err)
	}

	const topic = "pleiades.events.dedup.producer-side"
	evt, err := event.WrapPayload("retry-me", "dedup.producer-side", map[string]string{"attempt": "shared"})
	if err != nil {
		t.Fatalf("wrap payload: %v", err)
	}

	// Publish the identical, already-built Event twice, simulating a
	// caller retrying a publish that it could not confirm succeeded (e.g.
	// a timeout on the first attempt's response).
	if err := bus.Publish(ctx, topic, *evt); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if err := bus.Publish(ctx, topic, *evt); err != nil {
		t.Fatalf("publish 2 (retry): %v", err)
	}

	after, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("info after: %v", err)
	}

	gotStored := after.State.Msgs - before.State.Msgs
	if gotStored != 1 {
		t.Errorf("stream stored %d new messages for two Publish calls with the same Event, want exactly 1 (producer-side dedup via jetstream.WithMsgID did not suppress the retry)", gotStored)
	}
}

// TestNatsBusPublish_DistinctEventsBothStored proves the dedup window does
// not over-suppress: two genuinely different events (different IDs) to the
// same topic must both be stored, even if published back to back.
func TestNatsBusPublish_DistinctEventsBothStored(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url)
	if err != nil {
		t.Fatalf("failed to init bus: %v", err)
	}

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	stream, err := js.Stream(ctx, topology.StreamName)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	before, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("info before: %v", err)
	}

	const topic = "pleiades.events.dedup.distinct"
	evtA, err := event.WrapPayload("event-a", "dedup.distinct", map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("wrap payload a: %v", err)
	}
	evtB, err := event.WrapPayload("event-b", "dedup.distinct", map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("wrap payload b: %v", err)
	}

	if err := bus.Publish(ctx, topic, *evtA); err != nil {
		t.Fatalf("publish a: %v", err)
	}
	if err := bus.Publish(ctx, topic, *evtB); err != nil {
		t.Fatalf("publish b: %v", err)
	}

	after, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("info after: %v", err)
	}

	gotStored := after.State.Msgs - before.State.Msgs
	if gotStored != 2 {
		t.Errorf("stream stored %d new messages for two distinct events with identical content, want exactly 2 (dedup over-suppressed genuinely distinct events)", gotStored)
	}
}

// TestNatsDedupStore_SeenRecentlyAndMarkSeen proves the NATS-KV-backed
// DedupStore adapter against a real JetStream KV bucket, mirroring
// TestInProcessDedupStore's own coverage of the in-process adapter.
func TestNatsDedupStore_SeenRecentlyAndMarkSeen(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	kv, err := js.CreateOrUpdateKeyValue(ctx, topology.DedupBucketConfig())
	if err != nil {
		t.Fatalf("create kv bucket: %v", err)
	}

	store := event.NewNatsDedupStore(kv)

	seen, err := store.SeenRecently(ctx, "not-marked-yet")
	if err != nil {
		t.Fatalf("SeenRecently before mark: %v", err)
	}
	if seen {
		t.Error("SeenRecently reported true for a key that was never marked seen")
	}

	if err := store.MarkSeen(ctx, "now-marked", time.Hour); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	seen, err = store.SeenRecently(ctx, "now-marked")
	if err != nil {
		t.Fatalf("SeenRecently after mark: %v", err)
	}
	if !seen {
		t.Error("SeenRecently reported false immediately after MarkSeen")
	}
}
