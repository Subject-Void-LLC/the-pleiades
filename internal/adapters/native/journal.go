// Package native: the Walk tier's run journal sink.
//
// The Crawl tier writes its journal to a file next to the runbook. The
// Runner has no database and the process that has one is not the
// producer, so this side publishes instead: one message per topological
// level, onto the job's own journal subject, for the Controller to
// consume and store.
//
// Two things are stamped here and nowhere else. JobID comes from the
// dispatch this adapter was built for, never from anything the run
// itself reports, because a run cannot know which dispatch it is
// serving. Attempt comes from the delivery, through the context the
// Runner set it on, because JetStream's redelivery counter exists only
// on the message.
package native

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// journalPublisher is an engine.Journal that publishes entries onto the
// job's journal subject through the event bus.
//
// It is built per Execute call, which is what makes it safe for
// concurrent use without a lock of its own: the Agent runs several
// handleMessage workers at once, but each one builds its own Executor
// and its own publisher, so two of these never share state. A publisher
// held on the Adapter would need one.
type journalPublisher struct {
	// bus is the port this publishes through. Deliberately not a
	// jetstream handle: internal/adapters/native is not on
	// internal/archtest's adapter allowlist and must not import a
	// concrete driver.
	bus event.Bus

	// logger reports the one thing this decides rather than receives: a
	// dispatch that carried no attempt.
	logger *slog.Logger

	// jobID and deviceID identify the dispatch this publisher was built
	// for. They come from the payload, never from anything the run
	// reports, because a run cannot know which dispatch it serves.
	jobID    string
	deviceID string

	// attempt is the delivery's redelivery count, read from the context
	// once at construction rather than per Record.
	attempt int
}

// newJournalPublisher builds the sink for one dispatch.
//
// The attempt is read from ctx here, once, rather than on every Record:
// Record is handed a context the engine detached, and reading a value
// off it there would work but would make the sink's identity depend on
// which context a level barrier happened to carry.
func newJournalPublisher(ctx context.Context, bus event.Bus, logger *slog.Logger, jobID, deviceID string) *journalPublisher {
	attempt, ok := journal.AttemptFrom(ctx)
	if !ok {
		// Not an error. A caller that built this adapter outside the
		// Runner's own delivery path (a test, or a future direct
		// execution) has no redelivery count to give, and zero is what
		// JournalEntry documents for a tier with no dispatch. Logged at
		// debug so the absence is discoverable without being noise.
		logger.Debug("no dispatch attempt on the context, journaling this run as attempt zero",
			slog.String("job", jobID), slog.String("device", deviceID))
	}
	return &journalPublisher{bus: bus, logger: logger, jobID: jobID, deviceID: deviceID, attempt: attempt}
}

// Record implements engine.Journal by publishing one message per level.
//
// The context is used exactly as given. The engine already hands Record
// context.WithoutCancel of the run's own context plus its own timeout
// (recordLevel, internal/engine/journal_entry.go), so detaching again
// here would drop a deadline the sink is not allowed to lengthen, and
// treating a done context as run cancellation would be reading it
// backwards.
//
// An error returned from here never fails the run: the engine logs it,
// counts it, and returns the run's own outcome unchanged. That is not
// politeness about an observability side effect. On this tier an
// execution error routes into event.HandleDeliveryFailure, which Naks
// for redelivery, so a failed audit write that became an execution error
// would re-run the runbook against the same device.
func (p *journalPublisher) Record(ctx context.Context, entries []engine.JournalEntry) error {
	if len(entries) == 0 {
		return nil
	}

	stamped := make([]engine.JournalEntry, len(entries))
	for i, entry := range entries {
		entry.JobID = p.jobID
		entry.Attempt = p.attempt
		stamped[i] = entry
	}

	batch := journal.Batch{
		JobID:    p.jobID,
		DeviceID: p.deviceID,
		Attempt:  p.attempt,
		Entries:  stamped,
	}

	key := p.idempotencyKey(stamped)
	wrapped, err := event.WrapPayload(key, journal.EventType, batch)
	if err != nil {
		return fmt.Errorf("failed to wrap a journal batch for job %s: %w", p.jobID, err)
	}

	// A stable key, not a fresh uuid. publishJobEvent mints one per
	// event because a log line genuinely is a new thing each time; a
	// journal batch is not. A redelivered dispatch that re-runs the same
	// level would otherwise produce duplicate rows nothing can collapse,
	// which is the failure the store's own unique index exists to
	// prevent and which is cheaper to prevent here.
	if err := p.bus.Publish(event.WithIdempotencyKey(ctx, key), topology.JournalSubject(p.jobID), *wrapped); err != nil {
		return fmt.Errorf("failed to publish a journal batch for job %s: %w", p.jobID, err)
	}
	return nil
}

// idempotencyKey names this exact batch in a way a retry reproduces and
// a different batch does not.
//
// RunID is deliberately not part of it. The engine mints a fresh RunID
// on every Run call, so a redelivered dispatch running the same level
// again would produce a different key and defeat the whole point. What
// does identify a batch is the dispatch, the device, the attempt, and
// which entries of that run it carries, and Sequence is dense and
// monotonic across a run, so its first and last values name the level.
func (p *journalPublisher) idempotencyKey(entries []engine.JournalEntry) string {
	first := entries[0].Sequence
	last := entries[len(entries)-1].Sequence
	return fmt.Sprintf("journal:%s:%s:%d:%d-%d", p.jobID, p.deviceID, p.attempt, first, last)
}
