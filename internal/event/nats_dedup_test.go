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

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
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

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
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

// TestNatsBusPublish_DedupIsScopedToTheStreamNotTheSubject is the test
// whose absence let FAILURE_PATTERNS #222 ship.
//
// Both tests above publish to a single topic, so they are equally
// consistent with a subject-scoped dedup window and a stream-scoped one.
// They therefore PASSED while three separate doc comments in this module
// asserted the subject-scoped model, and a real Controller and a real
// Runner minted the same message id on two different subjects for five
// weeks. Every result the Runner published was discarded as a duplicate of
// the dispatch that caused it, and nothing anywhere said so.
//
// The distinction is not academic here. topology puts EVERY subject in one
// stream ("pleiades.>"), so a message id in this system is global: two
// publishers that agree on a key collide no matter how unrelated their
// subjects are.
//
// Asserting the mechanism rather than the bug, deliberately. A test that
// only pinned "the runner's result key differs from the dispatch's" would
// go green on the fix and say nothing about why the key has to differ.
func TestNatsBusPublish_DedupIsScopedToTheStreamNotTheSubject(t *testing.T) {
	url := startNatsContainer(t)
	ctx := context.Background()

	bus, err := event.NewNatsBus(ctx, url, nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
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

	// The real pair, spelled the way production spells it: a dispatch for
	// one device, and that device's result. Two unrelated subjects.
	const (
		dispatchTopic = "pleiades.jobs.dispatch.device-under-test"
		resultTopic   = "pleiades.jobs.results.job-under-test"
		sharedKey     = "job-under-test:device-under-test"
	)

	dispatch, err := event.WrapPayload(sharedKey, "job.dispatch", map[string]string{"device": "device-under-test"})
	if err != nil {
		t.Fatalf("wrap dispatch: %v", err)
	}
	result, err := event.WrapPayload(sharedKey, "job.result", map[string]string{"outcome": "completed"})
	if err != nil {
		t.Fatalf("wrap result: %v", err)
	}

	if err := bus.Publish(event.WithIdempotencyKey(ctx, sharedKey), dispatchTopic, *dispatch); err != nil {
		t.Fatalf("publish dispatch: %v", err)
	}
	// The publish that used to vanish. It returns nil either way, which is
	// the whole reason this was invisible: the broker answers a suppressed
	// duplicate with PubAck{Duplicate: true} and NO error.
	if err := bus.Publish(event.WithIdempotencyKey(ctx, sharedKey), resultTopic, *result); err != nil {
		t.Fatalf("publish result: %v", err)
	}

	after, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("info after: %v", err)
	}

	if stored := after.State.Msgs - before.State.Msgs; stored != 1 {
		t.Fatalf("stream stored %d messages for two subjects sharing one idempotency key, want exactly 1; "+
			"if this is 2 then JetStream's dedup window is no longer stream-scoped, and every doc comment "+
			"in this module that reasons from stream scope needs re-reading", stored)
	}

	// The other half, and the one that pins the fix: namespacing the key
	// is what makes the second publish land. Without this the test above
	// would be satisfied by a system that simply never delivers results.
	namespaced, err := event.WrapPayload("result:"+sharedKey, "job.result", map[string]string{"outcome": "completed"})
	if err != nil {
		t.Fatalf("wrap namespaced result: %v", err)
	}
	if err := bus.Publish(event.WithIdempotencyKey(ctx, "result:"+sharedKey), resultTopic, *namespaced); err != nil {
		t.Fatalf("publish namespaced result: %v", err)
	}

	final, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("info final: %v", err)
	}
	if stored := final.State.Msgs - after.State.Msgs; stored != 1 {
		t.Errorf("stream stored %d messages for a result published under a namespaced key, want exactly 1: "+
			"namespacing is what internal/runner relies on to report an outcome at all", stored)
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
