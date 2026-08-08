// Package runner: Agent's per-message handling, split into its own
// sibling file, following this repository's own file-per-concern
// convention (internal/dispatch's worker.go/worker_devices.go/
// worker_config.go split), once agent.go grew past AGENTS.md's
// ~300-line soft cap on logic files.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// errLockContention marks a failure to acquire payload.DeviceID's lock as
// contention (another execution genuinely holds it right now), distinct
// from a real adapter execution failure. handleMessage uses this
// distinction to Nak-and-retry a contended message rather than routing it
// through the Dead Letter Queue, which is reserved for a job that
// actually ran and failed. See executeWithLease (agent_exec.go), the one
// place a wrapped instance of this error originates
// (fmt.Errorf("%w: %w", errLockContention, cause), Go 1.20+'s
// multi-%w support, which is what still makes
// errors.Is(wrapped, errLockContention) match while a log line shows the
// real underlying lock.Manager error too -- no custom error type needed
// for either property).
var errLockContention = errors.New("device lock contention")

func (a *Agent) handleMessage(ctx context.Context, msg jetstream.Msg) {
	// The far end of PLAN.md Section 19's "API Request -> Event Bus ->
	// Runner Execution" trace. The publishing Bus left W3C trace context
	// in the message headers (internal/event/trace.go); extracting it here
	// makes this span a child of the API request's span, in the same
	// trace, rather than the root of a second one that no operator could
	// connect back to the request that caused it.
	ctx = event.ExtractTraceContext(ctx, msg.Headers())
	ctx, span := a.tracer.Start(ctx, "runner.handle_message",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attribute.String("messaging.destination.name", msg.Subject())),
	)
	defer span.End()

	// Every publish in this codebase goes through Bus.Publish now, which
	// wraps the domain payload in the Event envelope (internal/event) --
	// so the raw bytes on the wire are a marshaled Event, not a bare
	// DispatchPayload. natsBus.Subscribe callers get this unwrapped for
	// them automatically; Agent, which deliberately stays on its own pull
	// loop instead of going through Bus.Subscribe, has to do the same
	// two-step unwrap itself: decode the envelope, then decode
	// DispatchPayload out of its Data field.
	// Terminated, not Ack'd, on every decode failure below: a permanently
	// malformed payload will never become parseable no matter how many
	// times it is redelivered, so Ack (which would silently discard it
	// with no trace) and Nak (which would retry it forever) are both
	// wrong. Term matches natsBus.Subscribe's own identical handling of a
	// decode failure (internal/event/consumer.go). See termMalformed,
	// below, the one place this three-times-repeated shape lives.
	var evt event.Event
	if err := json.Unmarshal(msg.Data(), &evt); err != nil {
		a.termMalformed(msg, "dropping malformed message", err)
		return
	}

	var payload wire.DispatchPayload
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		a.termMalformed(msg, "dropping malformed message", err)
		return
	}

	// payload.JobID reaches topology.LogSubject/topology.ResultSubject by
	// bare string concatenation (native.Adapter.streamLog, agent_wal.go's
	// flushOne): the identical shape FAILURE_PATTERNS.md #63 already fixed
	// at the HTTP-facing StreamLogs boundary, where an unvalidated `>` or
	// `*` widened a NATS subject wildcard into every job's logs. Every
	// current producer of a DispatchPayload.JobID happens to be a
	// server-generated UUID, but dispatch.JobStore.Create's own contract
	// explicitly allows a caller-supplied JobID to override that default,
	// so nothing upstream actually guarantees this shape -- exactly the
	// "safe because of who usually produces it, not validated" trap #63's
	// own Lesson names. Checked here, once, before JobID reaches either
	// subject. A failing JobID can never become valid no matter how many
	// times it is redelivered, so this Terms the message, matching the
	// two malformed-payload branches immediately above.
	if _, err := uuid.Parse(payload.JobID); err != nil {
		a.termMalformed(msg, "dropping message with invalid job id", err, slog.String("job_id", payload.JobID))
		return
	}

	span.SetAttributes(
		attribute.String("pleiades.job.id", payload.JobID),
		attribute.String("pleiades.runbook.id", payload.RunbookID),
		attribute.String("pleiades.device.name", payload.DeviceName),
	)
	a.logger.Info("processing job",
		slog.String("runbook", payload.RunbookID),
		slog.String("device", payload.DeviceName),
	)

	// Execute the Runbook using the configured adapter, wrapped in a
	// per-device lock lease (PLAN.md Section 13) with a heartbeat-driven
	// self-abort (PLAN.md Section 16's Network Partitions mitigation).
	// See executeWithLease's own doc comment (agent_exec.go).
	execErr := a.executeWithLease(ctx, payload)
	if execErr != nil && errorsIsContention(execErr) {
		// Not an execution failure, so it is deliberately not reported to
		// the WAL below: another execution genuinely holds this device
		// right now, and Execute was never actually attempted. Nak with
		// backoff so this redelivers once the current holder's lease has
		// likely cleared, rather than routing through the Dead Letter
		// Queue, which is reserved for a job that actually ran and failed.
		a.logger.Warn("device lock contention, retrying later",
			slog.String("device_id", payload.DeviceID), slog.String("error", execErr.Error()))
		delay := a.calculateBackoff(numDeliveredFor(msg))
		if nakErr := msg.NakWithDelay(delay); nakErr != nil {
			a.logger.Error("failed to nak contended message", slog.String("error", nakErr.Error()))
		}
		return
	}

	// A genuine execution attempt happened (success or real failure):
	// record it in the WAL, if one is configured, before Ack/DLQ below.
	// See reportResult's own doc comment (agent_wal.go) for why this must
	// happen before Ack, not after: an Ack that the WAL append never
	// happened for would leave a Runner crash between the two silently
	// losing the outcome.
	a.reportResult(ctx, payload, execErr)

	if execErr != nil {
		span.RecordError(execErr)
		span.SetStatus(codes.Error, "adapter execution failed")
		a.logger.Error("adapter execution failed", slog.String("error", execErr.Error()))
		// Routed through the same Dead Letter Queue mechanism
		// natsBus.Subscribe itself uses (event.HandleDeliveryFailure),
		// rather than the previous "log it and ack" behavior, which
		// silently discarded every failed job on its very first attempt
		// with no operator-visible trail (PATTERNS.md's own Dead Letter
		// Queue entry named this exact line as the gap). Below
		// a.maxDeliver attempts this Naks with backoff for redelivery;
		// at or beyond it, the job is dead-lettered and terminated.
		if dlqErr := event.HandleDeliveryFailure(ctx, a.js, msg, a.maxDeliver, execErr.Error()); dlqErr != nil {
			a.logger.Error("failed to route failed delivery to dead letter handling", slog.String("error", dlqErr.Error()))
		}
		return
	}

	// Acknowledge the message as complete
	if err := msg.Ack(); err != nil {
		a.logger.Error("failed to ack successfully handled message", slog.String("error", err.Error()))
	}
}

// numDeliveredFor reads msg's own JetStream redelivery counter, mirroring
// event.HandleDeliveryFailure's identical "int(meta.NumDelivered)" usage
// exactly, so contention backoff grows with redelivery count the same way
// Dead Letter Queue backoff already does. A metadata read failure (only
// possible for a non-JetStream message, which cannot happen for anything
// this Agent hands itself) falls back to 0, the same "not yet redelivered"
// starting point calculateBackoff already treats attempt 0 as.
func numDeliveredFor(msg jetstream.Msg) int {
	meta, err := msg.Metadata()
	if err != nil {
		return 0
	}
	return int(meta.NumDelivered) // #nosec G115 -- NumDelivered is a JetStream redelivery counter, bounded in practice by a consumer's own small MaxDeliver; it cannot realistically approach int overflow range.
}

// errorsIsContention reports whether err is (or wraps) errLockContention.
// A tiny named wrapper around errors.Is so handleMessage's own branch
// reads as a single, self-explanatory condition.
func errorsIsContention(err error) bool {
	return errors.Is(err, errLockContention)
}

// termMalformed logs description alongside err and any extra slog args,
// then Terms msg: the shared shape of handleMessage's three
// permanently-malformed-payload branches (a corrupt envelope, a corrupt
// inner DispatchPayload, an invalid JobID), none of which can ever become
// valid no matter how many times msg is redelivered, so Ack (which would
// silently discard it with no trace) and Nak (which would retry it
// forever) are both wrong for any of them.
func (a *Agent) termMalformed(msg jetstream.Msg, description string, err error, extra ...any) {
	args := append([]any{slog.String("error", err.Error())}, extra...)
	a.logger.Error(description, args...)
	if termErr := msg.Term(); termErr != nil {
		a.logger.Error("failed to terminate message", slog.String("reason", description), slog.String("error", termErr.Error()))
	}
}
