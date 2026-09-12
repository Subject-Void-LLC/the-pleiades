// Package journal_test: the Controller-side consumer.
package journal_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// discardLogger is a logger for the cases that are not about logging.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// batchEvent wraps a batch the way the publisher does.
func batchEvent(t *testing.T, batch journal.Batch) event.Event {
	t.Helper()
	wrapped, err := event.WrapPayload("key-1", journal.EventType, batch)
	if err != nil {
		t.Fatalf("wrapping the batch: %v", err)
	}
	return *wrapped
}

func TestSubscriberStoresABatch(t *testing.T) {
	store, path := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())

	batch := journal.Batch{
		JobID: "job-1", DeviceID: "device-1", Attempt: 1,
		Entries: []engine.JournalEntry{
			walkEntry("job-1", "device-1", 1, 1, "tasks[0]"),
			walkEntry("job-1", "device-1", 1, 2, "tasks[1]"),
		},
	}
	if err := sub.Handle(batchEvent(t, batch)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if rows := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries"); rows[0] != "2" {
		t.Errorf("the table holds %v rows, want 2", rows)
	}
}

func TestSubscriberAcknowledgesAMalformedBatch(t *testing.T) {
	// It will never become decodable no matter how many times it is
	// redelivered, and retrying forever would block the consumer group
	// behind a message nothing can act on.
	store, path := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())

	evt := event.Event{ID: "key-1", Data: json.RawMessage(`{"entries": "not an array"}`)}
	if err := sub.Handle(evt); err != nil {
		t.Errorf("Handle asked for redelivery of a message that can never decode: %v", err)
	}
	if rows := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries"); rows[0] != "0" {
		t.Errorf("a malformed batch wrote %v rows", rows)
	}
}

func TestSubscriberAcknowledgesAnEmptyBatch(t *testing.T) {
	store, _ := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())
	if err := sub.Handle(batchEvent(t, journal.Batch{JobID: "job-1"})); err != nil {
		t.Errorf("Handle asked for redelivery of a batch with nothing in it: %v", err)
	}
}

func TestSubscriberAsksForRedeliveryWhenTheStoreFails(t *testing.T) {
	// The database being briefly unavailable is exactly the condition
	// redelivery exists for, so this failure kind is answered the
	// opposite way to a malformed payload.
	store, _ := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())

	bad := walkEntry("job-1", "device-1", 0, 1, "tasks[0]")
	bad.Outcome = engine.Outcome("teleported")
	batch := journal.Batch{JobID: "job-1", Entries: []engine.JournalEntry{bad}}

	if err := sub.Handle(batchEvent(t, batch)); err == nil {
		t.Error("Handle acknowledged a batch it failed to store")
	}
}

func TestSubscriberIsIdempotentAcrossARedelivery(t *testing.T) {
	// At-least-once delivery means the same batch can arrive twice. The
	// second arrival must be a no-op, not a failure that would ask for a
	// third.
	store, path := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())
	evt := batchEvent(t, journal.Batch{
		JobID: "job-1", DeviceID: "device-1",
		Entries: []engine.JournalEntry{walkEntry("job-1", "device-1", 0, 1, "tasks[0]")},
	})

	if err := sub.Handle(evt); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := sub.Handle(evt); err != nil {
		t.Fatalf("second delivery of the same batch: %v", err)
	}
	if rows := rawQuery(t, path, "SELECT COUNT(*) FROM journal_entries"); rows[0] != "1" {
		t.Errorf("a redelivered batch produced %v rows, want 1", rows)
	}
}

func TestSubscriberAttachesToTheJournalSubject(t *testing.T) {
	store, path := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())
	bus := event.NewInProcessBus()
	t.Cleanup(func() { _ = bus.Close() })
	ctx := context.Background()

	if err := sub.Subscribe(ctx, bus); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Published to the real subject the publisher uses, so a subscriber
	// listening on the wrong one fails here rather than in production.
	evt := batchEvent(t, journal.Batch{
		JobID: "job-1", DeviceID: "device-1",
		Entries: []engine.JournalEntry{walkEntry("job-1", "device-1", 0, 1, "tasks[0]")},
	})
	if err := bus.Publish(ctx, topology.JournalSubject("job-1"), evt); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	waitForRows(t, path, 1)
}

func TestNewSubscriberToleratesANilLogger(t *testing.T) {
	store, _ := newEntStore(t)
	sub := journal.NewSubscriber(store, nil)
	if err := sub.Handle(batchEvent(t, journal.Batch{JobID: "job-1"})); err != nil {
		t.Errorf("Handle: %v", err)
	}
}

// refusingBus is an event.Bus whose Subscribe always fails, so the
// startup path that must not silently continue can be tested.
type refusingBus struct{}

func (refusingBus) Publish(context.Context, string, event.Event) error { return nil }

func (refusingBus) Subscribe(context.Context, string, func(event.Event) error) error {
	return errSubscribeRefused
}

func (refusingBus) Close() error { return nil }

// errSubscribeRefused is refusingBus's fixed failure.
var errSubscribeRefused = errors.New("deliberate subscribe failure")

func TestSubscriberReportsAFailedSubscription(t *testing.T) {
	// The Controller treats this as fatal at startup. A controller that
	// silently stopped recording an audit trail is worse than one that
	// refuses to start, so the error has to actually come back.
	store, _ := newEntStore(t)
	sub := journal.NewSubscriber(store, discardLogger())

	err := sub.Subscribe(context.Background(), refusingBus{})
	if err == nil {
		t.Fatal("Subscribe reported success against a bus that refused it")
	}
	if !errors.Is(err, errSubscribeRefused) {
		t.Errorf("the error lost its cause: %v", err)
	}
}
