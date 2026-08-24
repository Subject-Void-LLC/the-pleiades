// Consumer-side duplicate suppression for dispatched work.
//
// JetStream's own producer-side deduplication remembers a Nats-Msg-Id
// only for the stream's duplicate window, and Phase 96c caps that window
// well below the outage budget because a second mechanism depends on it
// staying short: internal/dispatch.Reaper republishes a stranded job
// after ten minutes and relies on the original publish's window having
// closed. So the producer-side window cannot be stretched to cover a long
// outage, and something else has to.
//
// This is that something else. It sits in the Runner's own pull loop
// rather than behind event.Bus.Subscribe, which matters: the Agent
// deliberately pulls dispatch from a raw jetstream.Consumer, so
// event.NewIdempotentBus, the decorator built for exactly this purpose,
// could never have covered this path even once it was wired.
// FAILURE_PATTERNS.md #179 records that decorator and its store as fully
// built with no production caller at all. This consumes the store half,
// which is the half that was reusable.

package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// DedupStore is the subset of internal/event's port this package needs.
//
// It is redeclared here rather than imported so internal/runner depends
// on a behaviour rather than on internal/event's decorator, and so a test
// can substitute one without a KV bucket. The method set is identical, so
// event.NewNatsDedupStore satisfies it structurally.
type DedupStore interface {
	// SeenRecently reports whether key was marked seen within its TTL.
	SeenRecently(ctx context.Context, key string) (bool, error)
	// MarkSeen records key as seen, expiring after ttl.
	MarkSeen(ctx context.Context, key string, ttl time.Duration) error
}

// dispatchDedupKey is the identity of one unit of dispatched work.
//
// It is jobID plus deviceID, which is not a new invention: it is the
// exact key internal/dispatch stamps on the publish through
// event.WithIdempotencyKey, and the exact key internal/runner's own
// write-ahead log already derives for its result events. Three mechanisms
// agreeing on one identity is what makes suppression here meaningful
// rather than merely local.
func dispatchDedupKey(payload wire.DispatchPayload) string {
	return payload.JobID + ":" + payload.DeviceID
}

// alreadyExecuted reports whether this exact unit of work has already
// been executed to completion by this fleet.
//
// A store error is deliberately NOT treated as "already seen". The
// dedup bucket is an optimisation over a hazard that is itself rare, and
// refusing to run real work because a KV read failed would convert a
// storage blip into silently skipped automation. It returns false and
// logs, so the failure mode is the pre-Phase-96c behaviour rather than a
// new one.
func (a *Agent) alreadyExecuted(ctx context.Context, payload wire.DispatchPayload) bool {
	if a.dedup == nil {
		return false
	}

	key := dispatchDedupKey(payload)
	seen, err := a.dedup.SeenRecently(ctx, key)
	if err != nil {
		a.logger.Warn("could not check whether this dispatch was already executed, running it",
			slog.String("key", key), slog.String("error", err.Error()))
		return false
	}
	return seen
}

// markExecuted records that this unit of work completed, so a later
// redelivery of the same dispatch is suppressed.
//
// It is called ONLY after a successful execution, never before. Marking
// on receipt would make a first attempt that failed indistinguishable
// from one that succeeded, so a redelivery meant to retry real work would
// be silently dropped and the Dead Letter Queue path would stop being
// reachable. event.idempotentBus makes the same check-run-then-mark
// choice for the same reason.
func (a *Agent) markExecuted(ctx context.Context, payload wire.DispatchPayload) {
	if a.dedup == nil {
		return
	}

	key := dispatchDedupKey(payload)
	if err := a.dedup.MarkSeen(ctx, key, a.dedupTTL); err != nil {
		// Not fatal, and not worth failing a job that genuinely ran: the
		// consequence is that a redelivery of this dispatch would run
		// again, which is exactly the pre-Phase-96c behaviour.
		a.logger.Warn("could not record that this dispatch executed",
			slog.String("key", key), slog.String("error", err.Error()))
	}
}
