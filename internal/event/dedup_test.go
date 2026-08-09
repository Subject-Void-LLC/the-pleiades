package event_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
)

func TestDefaultIdempotencyKeyDerivation_IsEventID(t *testing.T) {
	evt := event.Event{ID: "some-uuid", Data: []byte(`{"k":"v"}`)}
	if got := event.DefaultIdempotencyKeyDerivation(evt); got != "some-uuid" {
		t.Errorf("DefaultIdempotencyKeyDerivation(%+v) = %q, want %q", evt, got, evt.ID)
	}
}

func TestDefaultIdempotencyKeyDerivation_DistinctEventsWithIdenticalContentDoNotCollide(t *testing.T) {
	// This is the regression case for the bug a real NATS container test
	// caught during this phase's own implementation: an earlier version of
	// this function hashed topic+Data instead of using ID, so two
	// different events publishing identical content (e.g. two health-check
	// pings a minute apart) derived the same key and JetStream's
	// producer-side dedup silently swallowed the second one.
	a := event.Event{ID: "event-a", Data: []byte(`{"status":"ok"}`)}
	b := event.Event{ID: "event-b", Data: []byte(`{"status":"ok"}`)}

	if event.DefaultIdempotencyKeyDerivation(a) == event.DefaultIdempotencyKeyDerivation(b) {
		t.Error("two distinct events with identical Data derived the same idempotency key")
	}
}

func TestDefaultIdempotencyKeyDerivation_RetryOfSameEventIsStable(t *testing.T) {
	// A caller-side retry resends the same already-built Event value; the
	// derived key must be identical across those calls, or JetStream's
	// producer-side dedup can never recognize the retry as a duplicate.
	evt := event.Event{ID: "retry-me", Data: []byte(`{"attempt":1}`)}

	first := event.DefaultIdempotencyKeyDerivation(evt)
	second := event.DefaultIdempotencyKeyDerivation(evt)
	if first != second {
		t.Errorf("DefaultIdempotencyKeyDerivation is not stable across calls with the same Event: got %q then %q", first, second)
	}
}

func TestInProcessDedupStore_SeenRecentlyBeforeMarkSeen(t *testing.T) {
	store := event.NewInProcessDedupStore()

	seen, err := store.SeenRecently(context.Background(), "never-marked")
	if err != nil {
		t.Fatalf("SeenRecently: %v", err)
	}
	if seen {
		t.Error("SeenRecently reported true for a key that was never marked seen")
	}
}

func TestInProcessDedupStore_MarkSeenThenSeenRecently(t *testing.T) {
	store := event.NewInProcessDedupStore()
	ctx := context.Background()

	if err := store.MarkSeen(ctx, "key-1", time.Hour); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}

	seen, err := store.SeenRecently(ctx, "key-1")
	if err != nil {
		t.Fatalf("SeenRecently: %v", err)
	}
	if !seen {
		t.Error("SeenRecently reported false immediately after MarkSeen with a 1-hour TTL")
	}
}

func TestInProcessDedupStore_ExpiresAfterTTL(t *testing.T) {
	store := event.NewInProcessDedupStore()
	ctx := context.Background()

	if err := store.MarkSeen(ctx, "key-1", 10*time.Millisecond); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	seen, err := store.SeenRecently(ctx, "key-1")
	if err != nil {
		t.Fatalf("SeenRecently: %v", err)
	}
	if seen {
		t.Error("SeenRecently reported true well past the key's TTL")
	}
}

// errDeliberate is a fixed sentinel used by the dedup-ordering regression
// test below to identify the deliberately-failing delivery.
var errDeliberate = errors.New("deliberate handler failure")

// TestNewIdempotentBus_FailureIsNotSwallowedAsAlreadySeen is the regression
// test for the ordering bug a design review caught before any code was
// written: marking a key seen BEFORE calling the real handler would make a
// first-attempt failure indistinguishable from a completed success on
// redelivery, since the decorator would see "already seen" on the retry and
// skip the handler entirely. This proves the actual, corrected ordering:
// the handler is invoked on every delivery until it returns nil, and only
// a nil return marks the key seen.
func TestNewIdempotentBus_FailureIsNotSwallowedAsAlreadySeen(t *testing.T) {
	inner := event.NewInProcessBus()
	store := event.NewInProcessDedupStore()
	bus := event.NewIdempotentBus(inner, store, time.Hour)

	var callCount int32
	handlerDone := make(chan error, 1)

	err := bus.Subscribe(context.Background(), "pleiades.events.dedup.ordering", func(e event.Event) error {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			// First delivery: fail. If the decorator marked the key seen
			// before this call, redelivery below would silently skip the
			// handler and this test would see callCount stay at 1.
			handlerDone <- errDeliberate
			return errDeliberate
		}
		// Second delivery (simulating the inner bus/broker redelivering
		// after a failure): succeed.
		handlerDone <- nil
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	evt := event.Event{ID: "redelivered-event", IdempotencyKey: "redelivered-event"}

	// First delivery: the in-process inner bus does not itself redeliver
	// (it has no broker underneath it to drive that), so this test drives
	// the "redelivery" by publishing the identical Event a second time,
	// standing in for what a real broker's redelivery would hand the
	// decorator: the same IdempotencyKey, again.
	if err := inner.Publish(context.Background(), "pleiades.events.dedup.ordering", evt); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	select {
	case err := <-handlerDone:
		if !errors.Is(err, errDeliberate) {
			t.Fatalf("first delivery: got err %v, want %v", err, errDeliberate)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first delivery")
	}

	if err := inner.Publish(context.Background(), "pleiades.events.dedup.ordering", evt); err != nil {
		t.Fatalf("publish 2 (simulated redelivery): %v", err)
	}
	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("second delivery: got err %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the second delivery; the handler was never called again, meaning the failed first attempt was incorrectly treated as already seen")
	}

	if got := atomic.LoadInt32(&callCount); got != 2 {
		t.Errorf("handler was called %d times, want exactly 2 (a wrongly-early mark-seen would have suppressed the second call)", got)
	}
}

// TestNewIdempotentBus_SuccessfulDeliveryIsNotRepeated proves the other
// half of the contract: once the handler succeeds, a later delivery of the
// same IdempotencyKey is skipped without invoking the handler again.
func TestNewIdempotentBus_SuccessfulDeliveryIsNotRepeated(t *testing.T) {
	inner := event.NewInProcessBus()
	store := event.NewInProcessDedupStore()
	bus := event.NewIdempotentBus(inner, store, time.Hour)

	var callCount int32
	called := make(chan struct{}, 4)

	err := bus.Subscribe(context.Background(), "pleiades.events.dedup.skip", func(e event.Event) error {
		atomic.AddInt32(&callCount, 1)
		called <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	evt := event.Event{ID: "already-processed", IdempotencyKey: "already-processed"}

	if err := inner.Publish(context.Background(), "pleiades.events.dedup.skip", evt); err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first delivery")
	}

	// Receiving from called only proves the caller-supplied handler body
	// ran (dedup.go's wrapped closure sends on it before calling
	// store.MarkSeen, not after), so the mark-seen write is not guaranteed
	// to have landed yet: wrapped keeps running in its own goroutine after
	// this test goroutine wakes up. Poll the same store idempotentBus
	// itself consults (SeenRecently) until the write is actually visible,
	// rather than assuming a fixed ordering that is not part of the
	// documented contract; a fixed sleep would just trade a rare failure
	// for a slow, still-technically-racy test.
	deadline := time.Now().Add(5 * time.Second)
	for {
		seen, err := store.SeenRecently(context.Background(), evt.IdempotencyKey)
		if err != nil {
			t.Fatalf("seen recently: %v", err)
		}
		if seen {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the first delivery's mark-seen write to become visible")
		}
		time.Sleep(time.Millisecond)
	}

	if err := inner.Publish(context.Background(), "pleiades.events.dedup.skip", evt); err != nil {
		t.Fatalf("publish 2 (duplicate): %v", err)
	}

	select {
	case <-called:
		t.Fatal("handler was called a second time for a duplicate delivery of an already-seen IdempotencyKey")
	case <-time.After(500 * time.Millisecond):
		// No second call arrived, as expected.
	}

	if got := atomic.LoadInt32(&callCount); got != 1 {
		t.Errorf("handler was called %d times, want exactly 1", got)
	}
}

// TestNewIdempotentBus_PublishDelegatesUnchanged proves Publish is passed
// straight through to the inner Bus: the decorator only wraps Subscribe.
func TestNewIdempotentBus_PublishDelegatesUnchanged(t *testing.T) {
	inner := event.NewInProcessBus()
	store := event.NewInProcessDedupStore()
	bus := event.NewIdempotentBus(inner, store, time.Hour)

	received := make(chan event.Event, 1)
	if err := inner.Subscribe(context.Background(), "pleiades.events.dedup.passthrough", func(e event.Event) error {
		received <- e
		return nil
	}); err != nil {
		t.Fatalf("subscribe on inner: %v", err)
	}

	if err := bus.Publish(context.Background(), "pleiades.events.dedup.passthrough", event.Event{ID: "passthrough-1"}); err != nil {
		t.Fatalf("publish via decorator: %v", err)
	}

	select {
	case got := <-received:
		if got.ID != "passthrough-1" {
			t.Errorf("received ID %q, want passthrough-1", got.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: Publish on the decorator did not reach the inner bus's subscriber")
	}
}
