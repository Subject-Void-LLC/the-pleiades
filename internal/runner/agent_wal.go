// Package runner: Agent's optional Write-Ahead-Log result buffering, the
// Runner-local half of PLAN.md Section 16's State Desync Mitigation ("the
// Runner buffers the result in a local Write-Ahead Log (WAL) and
// retries"). See wal.go's own doc comment for the interface this builds
// on and what it deliberately does not cover.
package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// walDurabilityTimeout bounds reportResult's own detached-context WAL
// write and eager flush (below): long enough for real disk I/O and one
// network publish attempt, short enough that a Runner shutting down does
// not hang indefinitely on it. See reportResult's own doc comment for why
// these two calls cannot simply use handleMessage's own ctx.
const walDurabilityTimeout = 10 * time.Second

// WithResultReporting makes Agent publish every execution outcome to
// topology.ResultSubject, which is what moves a job out of "running" once
// its devices have all reported.
//
// Without a WAL beside it (see WithResultWAL) reporting is best effort: an
// outcome that cannot be published at that instant is lost, and the job it
// belongs to waits for a device that will never report. Durability is the
// option. Reporting itself is not, which is the correction this made: it
// used to be reachable only through WithResultWAL, so a Runner started
// without RUNNER_WAL_DIR published no results at all. That was invisible
// while nothing consumed them and is not any more.
func WithResultReporting(bus event.Bus) AgentOption {
	return func(a *Agent) {
		a.bus = bus
	}
}

// WithResultWAL adds Write-Ahead-Log durability to that reporting: wal
// durably records every execution outcome before Agent attempts to
// report it, and bus is what a later flush publishes a still-pending
// entry through. bus must be an event.Bus, specifically, rather than a
// raw jetstream.JetStream.Publish, because the shared event.Bus
// idempotency mechanism (event.WithIdempotencyKey) is what protects a
// retried flush from double-publishing the same outcome twice -- exactly
// the correctness property a WAL-retry loop needs and a raw publish would
// not give it for free.
//
// Omitted by default: an Agent built with no WithResultWAL call appends
// nothing and flushes nothing. It still reports, if it was given a bus by
// WithResultReporting above.
func WithResultWAL(wal ResultWAL, bus event.Bus) AgentOption {
	return func(a *Agent) {
		a.wal = wal
		a.bus = bus
	}
}

// reportResult records execErr's outcome for payload in the WAL, if one
// is configured (WithResultWAL), and makes one immediate attempt to
// flush it. Called from handleMessage after a genuine execution attempt
// (success or real failure, never lock contention, which never invoked
// Execute at all) but before Ack/DLQ, so a Runner crash between "the job
// finished" and "the outcome was durably recorded" cannot happen: the
// WAL append is synchronous and fsync'd (fileWAL.Append) before
// handleMessage's own Ack ever runs.
//
// Deliberately does NOT use ctx, the context handleMessage received: that
// context is exactly what self-abort (agent_exec.go's heartbeat) cancels,
// and what Agent.Run's own graceful shutdown cancels while this in-flight
// call is still unwinding. A job that was canceled for either reason has
// already, genuinely, produced an outcome (an error, or a partial result)
// that the WAL exists specifically to durably capture -- the one scenario
// it must not silently skip is precisely "the process is going away right
// now." Using ctx here would make fileWAL.Append's own ctx.Err() guard
// (wal_file.go) reject the write outright the instant it is needed most,
// permanently losing an outcome that genuinely happened. This mirrors
// executeWithLease's own lease.Release(context.Background()) precedent
// (agent_exec.go) for the identical reason: cleanup/durability work that
// must survive the very cancellation that triggered it cannot be a child
// of that cancellation.
//
// A nil a.bus makes this a complete no-op: there is nowhere to report to.
// A nil a.wal reports without durability, which is the ordinary
// arrangement; see below.
func (a *Agent) reportResult(ctx context.Context, payload wire.DispatchPayload, execErr error) {
	if a.bus == nil {
		return
	}

	outcome, reason := "completed", ""
	if execErr != nil {
		outcome, reason = "failed", execErr.Error()
	}

	appendCtx, cancel := context.WithTimeout(context.Background(), walDurabilityTimeout)
	defer cancel()

	entry := ResultEntry{
		// A stable key derived from (JobID, DeviceID), not left blank for
		// fileWAL.Append to mint a fresh random UUID: this is what stays
		// the same across a JetStream redelivery of the identical
		// dispatch (a crash between this Runner's own local
		// wal.Acknowledge succeeding and msg.Ack() reaching the broker).
		// event.DefaultIdempotencyKeyDerivation's own doc comment
		// (internal/event/dedup.go) names exactly this class of mistake:
		// a fresh random ID per attempt would let the re-executed,
		// redelivered job publish as an entirely distinct job.result
		// event that no idempotency-key dedup (event.Bus's own
		// IdempotencyKey mechanism) could ever collapse back down to one,
		// since dedup only recognizes a key it has seen before. Mirrors
		// internal/dispatch/worker_devices.go's own identical
		// JobID+":"+DeviceID key exactly, for the identical reason.
		ID:        payload.JobID + ":" + payload.DeviceID,
		JobID:     payload.JobID,
		DeviceID:  payload.DeviceID,
		RunbookID: payload.RunbookID,
		Outcome:   outcome,
		Reason:    reason,
	}

	// Without a WAL the outcome is published directly, best effort: if
	// the bus is unreachable at this instant the result is lost and the
	// job it belongs to waits for a device that will never report.
	//
	// That is worse than the WAL path and it is still far better than the
	// alternative this replaced, which was publishing nothing at all
	// unless an operator had set RUNNER_WAL_DIR. Reporting was opt-in
	// while nothing consumed it, so the gap was invisible; now that a job
	// stays "running" until its devices report, a Runner that reports
	// nothing leaves every job it touches running forever. Durability is
	// the option here. Reporting is not.
	if a.wal == nil {
		publishCtx, publishCancel := context.WithTimeout(context.Background(), walDurabilityTimeout)
		defer publishCancel()
		a.publishResult(publishCtx, entry)
		return
	}

	entry, err := a.wal.Append(appendCtx, entry)
	if err != nil {
		a.logger.Error("failed to append wal result entry", slog.String("job_id", payload.JobID), slog.String("error", err.Error()))
		return
	}

	// Eager first attempt, also detached from ctx for the identical
	// reason as the Append call above. A failure here just leaves entry
	// Pending for flushWAL's own next idle-tick retry (fetchLoop,
	// agent_run.go), from a future, healthy Agent.Run call if this
	// process is shutting down right now; it is not itself an error this
	// call needs to report further, since the durable append above
	// already succeeded.
	flushCtx, flushCancel := context.WithTimeout(context.Background(), walDurabilityTimeout)
	defer flushCancel()
	a.flushOne(flushCtx, entry)
}

// publishResult publishes one outcome to topology.ResultSubject, reporting
// whether it landed. Shared by the WAL flush and the WAL-less direct path,
// so both derive the subject and the idempotency key identically.
func (a *Agent) publishResult(ctx context.Context, entry ResultEntry) bool {
	evt, err := event.WrapPayload(entry.ID, "job.result", entry)
	if err != nil {
		a.logger.Error("failed to wrap result entry", slog.String("id", entry.ID), slog.String("error", err.Error()))
		return false
	}

	// entry.ID as the idempotency key: it is derived from
	// (JobID, DeviceID), so a redundant retry of an already-delivered
	// outcome dedups server-side via the shared event.Bus mechanism
	// instead of reporting the same job outcome twice.
	pubCtx := event.WithIdempotencyKey(ctx, entry.ID)
	if err := a.bus.Publish(pubCtx, topology.ResultSubject(entry.JobID), *evt); err != nil {
		a.logger.Debug("failed to publish result entry",
			slog.String("id", entry.ID), slog.String("job_id", entry.JobID), slog.String("error", err.Error()))
		return false
	}
	return true
}

// flushWAL retries delivering every entry still Pending in the WAL. It is
// called from fetchLoop's own idle-backoff branch: an idle fetch tick has
// spare cycles and is exactly the moment worth checking whether a prior
// publish failure (a bus outage) has since cleared. A nil a.wal makes
// this a no-op.
func (a *Agent) flushWAL(ctx context.Context) {
	if a.wal == nil {
		return
	}

	pending, err := a.wal.Pending(ctx)
	if err != nil {
		a.logger.Error("failed to read pending wal entries", slog.String("error", err.Error()))
		return
	}
	for _, entry := range pending {
		a.flushOne(ctx, entry)
	}
}

// flushOne attempts to publish entry to topology.ResultSubject(entry.
// JobID) and, on success, acknowledges it in the WAL so it does not
// appear in a later Pending call. A publish failure leaves entry pending,
// silently: the caller (reportResult's eager attempt, or flushWAL's own
// retry loop) does not treat this as an error of its own, since a future
// flushWAL call is exactly the retry mechanism for it.
func (a *Agent) flushOne(ctx context.Context, entry ResultEntry) {
	if !a.publishResult(ctx, entry) {
		return
	}

	if err := a.wal.Acknowledge(ctx, entry.ID); err != nil {
		// Flushed but not locally acknowledged: the next flushWAL call
		// will republish it, deduped server-side by the identical
		// IdempotencyKey above, not silently dropped or double-counted.
		a.logger.Error("flushed wal entry but failed to acknowledge it locally; a future retry will redeliver a deduped duplicate",
			slog.String("id", entry.ID), slog.String("error", err.Error()))
	}
}
