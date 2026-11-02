// Package entstore: the Controller side of the Walk tier's facts, as
// internal/journal's Subscriber is the journal's. A Runner publishes a
// facts.Batch per task that emitted facts; one durable consumer, shared by
// every Controller replica, stores it.
package entstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// Subscriber writes published fact batches into the store.
type Subscriber struct {
	store  facts.Store
	logger *slog.Logger
	// admit decides whether a batch's job and device are ones the
	// Controller dispatched, the same check the journal's consumer makes
	// (internal/journal's admission.go): any Runner may publish on any
	// job's facts subject.
	admit journal.AdmissionCheck
}

// NewSubscriber builds the consumer over store, storing only batches admit
// admits; a nil admit stores every well-formed batch, which only a test of
// the store alone wants.
func NewSubscriber(store facts.Store, logger *slog.Logger, admit journal.AdmissionCheck) *Subscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &Subscriber{store: store, logger: logger, admit: admit}
}

// Subscribe attaches s to every job's facts subject: one durable consumer
// group across replicas, so each batch is stored once.
func (s *Subscriber) Subscribe(ctx context.Context, bus event.Bus) error {
	if err := bus.Subscribe(ctx, topology.FactsSubjectAll(), s.Handle); err != nil {
		return fmt.Errorf("failed to subscribe the facts consumer: %w", err)
	}
	return nil
}

// Handle decodes one batch and stores what passes. A batch that cannot be
// decoded, names no job or device, holds nothing that passes, names a device
// the database lacks, or was never dispatched is acknowledged and logged:
// redelivery would never change it. A database failure, or a dispatch not
// recorded yet, is returned, so the bus redelivers.
func (s *Subscriber) Handle(evt event.Event) error {
	var batch facts.Batch
	if err := json.Unmarshal(evt.Data, &batch); err != nil || batch.JobID == "" || batch.DeviceID == "" {
		s.logger.Error("dropping a malformed facts batch", slog.String("event", evt.ID))
		return nil
	}
	entries, refused, err := facts.Verify(batch.Entries)
	if err != nil || refused > 0 {
		s.logger.Warn("dropping facts a Runner published that do not pass the rules",
			slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.Int("refused", refused), slog.Any("error", err))
	}
	if len(entries) == 0 {
		return nil
	}
	if s.admit != nil {
		admission, err := s.admit(context.Background(), batch.JobID, batch.DeviceID)
		if err != nil {
			return fmt.Errorf("failed to check the facts batch for job %s: %w", batch.JobID, err)
		}
		switch admission {
		case journal.AdmitDispatched:
		case journal.AdmitNotDispatched:
			s.logger.Warn("dropping a facts batch for a device this controller never dispatched",
				slog.String("job", batch.JobID), slog.String("device", batch.DeviceID))
			return nil
		default:
			return fmt.Errorf("the facts batch names job %s device %s, which this controller has not recorded dispatching yet", batch.JobID, batch.DeviceID)
		}
	}
	if err := s.store.Record(context.Background(), batch.DeviceID, entries, batch.ObservedAt); err != nil {
		if errors.Is(err, ErrUnknownDevice) {
			s.logger.Error("dropping a facts batch for a device the database does not hold",
				slog.String("job", batch.JobID), slog.String("device", batch.DeviceID))
			return nil
		}
		return fmt.Errorf("failed to store the facts for job %s: %w", batch.JobID, err)
	}
	s.logger.Debug("stored a facts batch", slog.String("job", batch.JobID), slog.String("device", batch.DeviceID), slog.Int("facts", len(entries)))
	return nil
}
