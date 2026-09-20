// Package dispatch: the Controller side of a job's results.
//
// A Runner publishes one outcome per device onto that job's result
// subject. This is what goes on the other end: one durable consumer,
// shared by every Controller replica, folding each outcome onto the task
// row it belongs to and ending the job once every dispatched device has
// reported.
//
// Without it the result subject is a decoration, which is exactly what it
// was: Runners had been publishing onto it since Phase 15 and nothing in
// the module ever subscribed, so every outcome lived for the stream's
// retention window and then did not. A job could not truthfully be called
// "running" until something was listening.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// resultPayload is the wire shape internal/runner's ResultEntry publishes.
//
// Declared here rather than imported, deliberately. internal/runner is a
// separate binary's package and importing it from the Controller side
// would couple two processes' builds; this mirrors the identical
// hand-synced split internal/api and internal/dispatch already use for the
// job.requested payload. The json tags are the contract, and the field
// names match ResultEntry's one for one.
type resultPayload struct {
	ID       string `json:"id"`
	JobID    string `json:"job_id"`
	DeviceID string `json:"device_id"`
	// Outcome is the Runner's vocabulary, "completed" or "failed", which
	// is not this package's Result vocabulary. resultFor below is the one
	// place the two meet.
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`

	// Unchecked is how many tasks a check could not check on the device
	// (ResultEntry.Unchecked), absent from a Runner that predates it.
	Unchecked int `json:"unchecked,omitempty"`
}

// ResultConsumer folds published per-device outcomes back onto their job.
type ResultConsumer struct {
	store JobStore

	// logger carries the two things this consumer can only report and
	// never fix: a message that cannot be decoded, and one naming a job
	// or device this Controller has no record of. Both are acknowledged,
	// so the log line is the only trace either happened.
	logger *slog.Logger
}

// NewResultConsumer builds the consumer-side handler over store.
func NewResultConsumer(store JobStore, logger *slog.Logger) *ResultConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResultConsumer{store: store, logger: logger}
}

// Subscribe attaches c to the result subject on bus.
//
// The subject is the wildcard over every job, and event.Bus.Subscribe
// derives a durable consumer name from it, so every Controller replica
// joins one consumer group and each result is folded in once rather than
// once per replica.
func (c *ResultConsumer) Subscribe(ctx context.Context, bus event.Bus) error {
	if err := bus.Subscribe(ctx, topology.ResultSubjectAll(), c.Handle); err != nil {
		return fmt.Errorf("failed to subscribe the job result consumer: %w", err)
	}
	return nil
}

// Handle decodes one published result and records it.
//
// The error return drives at-least-once delivery: nil acknowledges, and
// non-nil routes into the adapter's redelivery and dead-letter policy. The
// failure kinds are answered differently on purpose, following
// internal/journal's own consumer exactly.
//
// A message that cannot be decoded, or that names an outcome this build
// does not recognize, is acknowledged rather than retried. Neither will
// ever become valid, and retrying one forever parks a poison message at
// the head of a consumer group shared by every job in the system.
//
// A result naming a job or device this Controller has no dispatched task
// for is also acknowledged, and this is the case worth being careful
// about. It looks like a transient miss and is not: the fan-out writes
// every task row before the dispatch that produces a result can be
// executed, so by the time any result exists its row already does. What it
// really means is a job deleted underneath a run, or a stale message from
// beyond the stream's retention window. Retrying would block every other
// job's results behind it.
//
// A store failure IS retried, because a database being briefly
// unavailable is exactly what redelivery exists for.
func (c *ResultConsumer) Handle(evt event.Event) error {
	var payload resultPayload
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		c.logger.Error("dropping a malformed job result",
			slog.String("event", evt.ID),
			slog.String("error", err.Error()))
		return nil
	}

	result, err := resultFor(payload.Outcome)
	if err != nil {
		c.logger.Error("dropping a job result naming an outcome this build does not recognize",
			slog.String("event", evt.ID),
			slog.String("job_id", payload.JobID),
			slog.String("device_id", payload.DeviceID),
			slog.String("error", err.Error()))
		return nil
	}
	if payload.Unchecked < 0 {
		// Not a count, so the message is malformed, and like an unknown
		// outcome it will never become valid: retrying it would park it at
		// the head of the consumer group.
		c.logger.Error("dropping a job result with a negative unchecked count",
			slog.String("event", evt.ID),
			slog.String("job_id", payload.JobID),
			slog.String("device_id", payload.DeviceID),
			slog.Int("unchecked", payload.Unchecked))
		return nil
	}

	// A fresh context rather than one carried from the caller:
	// event.Bus.Subscribe hands over a decoded Event and no context, and
	// these writes must not inherit a deadline nobody set for them.
	ctx := context.Background()

	complete, err := c.store.RecordResult(ctx, payload.JobID, payload.DeviceID, result, payload.Reason, payload.Unchecked)
	switch {
	case errors.Is(err, ErrJobNotFound):
		c.logger.Warn("dropping a job result for a job or device this controller has no dispatched task for",
			slog.String("job_id", payload.JobID),
			slog.String("device_id", payload.DeviceID))
		return nil
	case err != nil:
		return fmt.Errorf("failed to record result for device %s on job %s: %w", payload.DeviceID, payload.JobID, err)
	}

	if !complete {
		return nil
	}

	// Every dispatched device has now reported. CompleteRunning is
	// conditioned on the job still being "running", so it is safe for two
	// results that both believe they were last to both arrive here, and
	// safe for this to run again on a redelivery.
	if err := c.store.CompleteRunning(ctx, payload.JobID); err != nil {
		if errors.Is(err, ErrJobNotFound) {
			c.logger.Warn("the last result arrived for a job that no longer exists",
				slog.String("job_id", payload.JobID))
			return nil
		}
		return fmt.Errorf("failed to complete job %s: %w", payload.JobID, err)
	}
	return nil
}

// resultFor maps the Runner's outcome vocabulary onto this package's.
//
// The two differ, and the translation lives in exactly one place for that
// reason. A Runner says "completed" for a run that finished without error,
// where this package says "succeeded": "completed" is already a JOB state
// here and means something else entirely, so carrying the Runner's word
// any further inward would put one string with two meanings into the same
// package.
func resultFor(outcome string) (Result, error) {
	switch outcome {
	case "completed":
		return ResultSucceeded, nil
	case "failed":
		return ResultFailed, nil
	default:
		return "", fmt.Errorf("unknown runner outcome: %q", outcome)
	}
}
