// Package journal: the Controller side of the Walk tier's journal.
//
// A Runner publishes a Batch per topological level onto the job's
// journal subject. This is what goes on the other end: one durable
// consumer, shared by every Controller replica, writing what it receives
// into the control plane's database.
//
// Without it the port is a decoration. A Runner that publishes into a
// stream nobody consumes produces a journal that exists for the stream's
// retention window and then does not.
package journal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// Subscriber writes published journal batches into the store.
type Subscriber struct {
	store  *EntStore
	logger *slog.Logger
}

// NewSubscriber builds the consumer-side handler over store.
func NewSubscriber(store *EntStore, logger *slog.Logger) *Subscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &Subscriber{store: store, logger: logger}
}

// Subscribe attaches s to the journal subject on bus.
//
// The subject is the wildcard over every job, and event.Bus.Subscribe
// derives a durable consumer name from it, so every Controller replica
// joins one consumer group and each batch is written once rather than
// once per replica.
func (s *Subscriber) Subscribe(ctx context.Context, bus event.Bus) error {
	if err := bus.Subscribe(ctx, topology.JournalSubjectAll(), s.Handle); err != nil {
		return fmt.Errorf("failed to subscribe the run journal consumer: %w", err)
	}
	return nil
}

// Handle decodes one published batch and stores it.
//
// The error return is what makes at-least-once delivery work: a nil
// error acknowledges the message, a non-nil error routes into the bus
// adapter's own redelivery and dead-letter policy. So the two failure
// kinds are answered differently on purpose.
//
// A batch that cannot be decoded is acknowledged, not retried. It will
// never become decodable no matter how many times it is redelivered, and
// retrying it forever would block the consumer group behind a message
// nothing can act on. It is logged at error, which is the only useful
// thing left to do with it, and this matches how every other consumer in
// this codebase treats a malformed payload.
//
// A store failure IS retried, because the database being briefly
// unavailable is exactly the condition redelivery exists for. With one
// exception, which the hardening audit found rather than review: a batch
// the store marks ErrUnstorable carries a value no column can hold, so
// it will be refused identically forever. Retrying that parks a poison
// message at the head of the consumer group and blocks every batch
// behind it, which is a worse outcome than losing the one batch that was
// already unusable. It is acknowledged and logged at error, exactly like
// an undecodable one.
func (s *Subscriber) Handle(evt event.Event) error {
	var batch Batch
	if err := json.Unmarshal(evt.Data, &batch); err != nil {
		s.logger.Error("dropping a malformed run journal batch",
			slog.String("event", evt.ID),
			slog.String("error", err.Error()))
		return nil
	}
	if len(batch.Entries) == 0 {
		// A publisher does not send one, so this is either a bug
		// upstream or a hand-crafted message. Either way there is
		// nothing to write and nothing redelivery would fix.
		s.logger.Warn("received a run journal batch with no entries",
			slog.String("event", evt.ID),
			slog.String("job", batch.JobID))
		return nil
	}

	// A fresh context rather than one carried from the handler's caller:
	// event.Bus.Subscribe hands a decoded Event and no context, and the
	// write must not inherit a deadline nobody set for it.
	written, err := s.store.Save(context.Background(), batch.Entries)
	if err != nil && errors.Is(err, ErrUnstorable) {
		s.logger.Error("dropping a run journal batch the store can never accept",
			slog.String("job", batch.JobID),
			slog.String("device", batch.DeviceID),
			slog.Int("attempt", batch.Attempt),
			slog.Int("entries", len(batch.Entries)),
			slog.Int("written", written),
			slog.String("error", err.Error()))
		return nil
	}
	if err != nil {
		s.logger.Error("failed to store a run journal batch",
			slog.String("job", batch.JobID),
			slog.String("device", batch.DeviceID),
			slog.Int("attempt", batch.Attempt),
			slog.Int("entries", len(batch.Entries)),
			slog.Int("written", written),
			slog.String("error", err.Error()))
		return fmt.Errorf("failed to store the run journal for job %s: %w", batch.JobID, err)
	}

	// Logged at debug rather than info: this fires once per topological
	// level of every task of every job, which is the highest-volume
	// event in the system.
	s.logger.Debug("stored a run journal batch",
		slog.String("job", batch.JobID),
		slog.String("device", batch.DeviceID),
		slog.Int("attempt", batch.Attempt),
		slog.Int("written", written),
		slog.Int("skipped", len(batch.Entries)-written))
	return nil
}
