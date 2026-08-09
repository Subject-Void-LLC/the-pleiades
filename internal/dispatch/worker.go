// Package dispatch: Worker, the durable job.requested consumer that
// performs the actual per-device fan-out a job's launch only records the
// intent for.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// HandleJobRequested is a job.requested handler, exactly the signature
// event.Bus.Subscribe requires (func(event.Event) error). A nil return
// acknowledges the delivery; a non-nil return triggers the bus adapter's
// own redelivery/dead-letter policy (see internal/event/consumer.go).
//
// This handler signature carries no per-message request context (unlike
// an HTTP handler's r.Context()), so every call inside it uses a context
// derived from context.Background(), explicitly, rather than one handed
// in. This mirrors internal/api/dispatcher.go's own existing,
// already-justified use of a detached background context for its
// per-device publishes: "an HTTP client disconnecting mid-loop should not
// cancel dispatches already queued for other devices." The same reasoning
// applies here a level up: the NATS delivery that invoked this handler
// completing, timing out, or being redelivered must not cancel work this
// call is the sole owner of once BeginFanOut has claimed it (see below),
// so no ctx derived from the delivery itself is threaded through.
//
// The context is not, however, left unbounded: it carries a
// context.WithTimeout of exactly w.fanOutLeaseTTL. A hung downstream call
// (a stalled DB query, a NATS publish that never returns) would otherwise
// block this handler forever, which is also precisely what lets this
// Worker's own heartbeat go stale long enough for a second delivery's
// BeginFanOut to legitimately reclaim the job out from under it (see
// DefaultFanOutLeaseTTL's own doc comment and JobStore.BeginFanOut's
// staleAfter reclaim). Tying the bound to that same fanOutLeaseTTL,
// rather than some other arbitrary duration, keeps the two consistent: a
// hang that would outlive its own lease anyway should be canceled here
// and now, not left running past the exact point another worker is
// already entitled to take over. The fencing token this call's own
// BeginFanOut obtains (see fence below) is the backstop for the rare case
// a call is already in-flight, uncancellable, at the moment the timeout
// fires: any write it still manages to complete after that point is
// rejected by the storage layer via ErrFenced rather than trusted to have
// already stopped.
func (w *Worker) HandleJobRequested(evt event.Event) error {
	ctx, cancel := context.WithTimeout(context.Background(), w.fanOutLeaseTTL)
	defer cancel()

	var payload jobRequestedPayload
	if err := json.Unmarshal(evt.Data, &payload); err != nil {
		// A malformed payload can never become parseable no matter how
		// many times it is redelivered, but this handler has no
		// terminate-the-message primitive of its own (that lives in the
		// bus adapter, see internal/event/consumer.go's own Term-on-
		// decode-failure handling for the envelope itself). Returning an
		// error here is still the right behavior for this narrower,
		// inner decode of Data, since it is the only signal this
		// function's own contract can give the adapter.
		return fmt.Errorf("failed to decode job.requested payload: %w", err)
	}

	began, fence, err := w.store.BeginFanOut(ctx, payload.JobID, w.fanOutLeaseTTL)
	if err != nil {
		return fmt.Errorf("failed to begin fan-out for job %s: %w", payload.JobID, err)
	}
	if !began {
		// Either another delivery of this same job.requested event is
		// plausibly still actively claiming (or already finished) this
		// job's fan-out, or the job is already terminal. Acking here
		// rather than reprocessing is exactly what BeginFanOut's own
		// idempotency guard exists for: at-least-once delivery must not
		// fan out to every device twice. A job actually abandoned by a
		// crashed Worker is not lost: BeginFanOut's own staleAfter reclaim
		// (w.fanOutLeaseTTL) lets a later redelivery of this same event
		// claim it instead once its heartbeat goes quiet for long enough.
		return nil
	}

	job, priorTasks, err := w.store.Get(ctx, payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to load job %s after claiming fan-out: %w", payload.JobID, err)
	}

	// alreadyRecorded names every device an attempt this claim might be
	// superseding already recorded (see BeginFanOut's own doc comment on
	// why this guard lives here rather than inside JobStore.RecordTask).
	// On the overwhelming majority of claims, a fresh pending job, this is
	// always empty (RecordTask is never called before BeginFanOut
	// succeeds), so building it costs nothing beyond the Get call above,
	// already made on every claim regardless. Only a BeginFanOut stale
	// reclaim, re-running this whole loop for a job a crashed Worker
	// partially finished, ever finds entries here.
	alreadyRecorded := make(map[string]struct{}, len(priorTasks))
	var dispatched, skipped, failed int
	for _, t := range priorTasks {
		alreadyRecorded[t.DeviceID] = struct{}{}
		switch t.Outcome {
		case OutcomeDispatched:
			dispatched++
		case OutcomeSkipped:
			skipped++
		case OutcomeFailed:
			failed++
		}
	}

	rb, err := w.runbooks.Get(ctx, job.RunbookID)
	if err != nil {
		// This is a real, permanent failure case the state enum could not
		// honestly represent before internal/ent/schema/job.go added a
		// "failed" state alongside "completed": a runbook that cannot be
		// resolved means fan-out never even considered a single device,
		// which "completed" with all-zero tallies would misreport as
		// indistinguishable from a legitimate dispatch against an empty
		// group. See that schema's own State field comment for the full
		// reasoning.
		//
		// The reason recorded on the job names only the runbook_id, never
		// err's own text: err can carry a filesystem path or another
		// storage-layer detail (runbook.Source.Get's own doc comment),
		// and Job.failure_reason is a caller-readable audit field, not a
		// server log. The full error is logged server-side only, the same
		// posture internal/api/dispatcher.go already takes with the
		// repository's own error text.
		reason := fmt.Sprintf("runbook %q could not be resolved", job.RunbookID)
		if errors.Is(err, runbook.ErrNotFound) {
			reason = fmt.Sprintf("runbook %q not found", job.RunbookID)
		}
		slog.Error("job fan-out failed to resolve runbook",
			slog.String("job_id", job.JobID),
			slog.String("runbook_id", job.RunbookID),
			slog.String("error", err.Error()))

		// A retry cannot help past this point even for a transient
		// resolution error: BeginFanOut above already moved this job out
		// of "pending", so any redelivery of this same job.requested
		// event will see began == false and skip without doing any work
		// (the idempotency guard, by design, cannot distinguish "someone
		// else is already handling it" from "the one attempt that ever
		// will happen already failed"). This call is the job's only
		// chance to reach a terminal state, so the failure is recorded
		// here and the delivery is acked (nil return), not retried.
		if failErr := w.store.Fail(ctx, job.JobID, fence, reason); failErr != nil {
			if fenced(job.JobID, failErr) {
				return nil
			}
			return fmt.Errorf("failed to record job %s as failed: %w", job.JobID, failErr)
		}
		return nil
	}

	iter, err := w.repo.GetGroup(ctx, pkginventory.Selector{GroupName: job.GroupName})
	if err != nil {
		// Mirrors internal/api/dispatcher.go's own posture: the
		// repository's own error text is logged server-side only, never
		// surfaced onto the job resource, since it can name tables,
		// columns, and hosts.
		slog.Error("job fan-out failed to query inventory",
			slog.String("job_id", job.JobID),
			slog.String("group", job.GroupName),
			slog.String("error", err.Error()))
		if failErr := w.store.Fail(ctx, job.JobID, fence, fmt.Sprintf("failed to query group %q", job.GroupName)); failErr != nil {
			if fenced(job.JobID, failErr) {
				return nil
			}
			return fmt.Errorf("failed to record job %s as failed: %w", job.JobID, failErr)
		}
		return nil
	}
	defer iter.Close()

	// The streaming Next/Item/Error/Close loop shape, mirroring
	// internal/api/dispatcher.go's CURRENT loop exactly. Devices are
	// never materialized into a slice: this streaming property is the
	// literal thing Phase 14's own 10,000-device Release Gate measures,
	// and buffering the whole group here would defeat the point of
	// moving fan-out into a durable worker in the first place.
	for iter.Next(ctx) {
		device := iter.Item()

		if _, done := alreadyRecorded[string(device.ID())]; done {
			// This exact device was already admitted-or-skipped and
			// recorded (its outcome already folded into
			// dispatched/skipped/failed above) by the attempt this
			// BeginFanOut stale reclaim is superseding. Redoing admission,
			// republishing, or recording it again would double both its
			// audit trail and, for an already-dispatched device, the
			// runbook.dispatched event on the bus (dedup by
			// IdempotencyKey, event.WithIdempotencyKey below, would catch
			// a redundant publish, but never even attempting it is
			// simpler and cheaper than relying on that as the only
			// backstop).
			continue
		}

		// admitAndDispatchDevice (worker_devices.go) owns admission,
		// payload construction, publish, and the RecordTask write for
		// exactly one device; see that file's own doc comment for why
		// this block lives there rather than inline in this loop.
		outcome, err := w.admitAndDispatchDevice(ctx, job, fence, rb, evt, device)
		if err != nil {
			if fenced(job.JobID, err) {
				return nil
			}
			return err
		}
		switch outcome {
		case OutcomeDispatched:
			dispatched++
		case OutcomeSkipped:
			skipped++
		case OutcomeFailed:
			failed++
		}
	}

	if err := iter.Error(); err != nil {
		// Mirrors internal/api/dispatcher.go's own defensive posture: the
		// iterator's own message is logged, never surfaced onto the job
		// resource, for the identical reason its error above is not
		// surfaced either.
		slog.Error("job fan-out iterator failed mid-dispatch",
			slog.String("job_id", job.JobID),
			slog.String("error", err.Error()))
		if failErr := w.store.Fail(ctx, job.JobID, fence, "inventory iteration failed mid-dispatch"); failErr != nil {
			if fenced(job.JobID, failErr) {
				return nil
			}
			return fmt.Errorf("failed to record job %s as failed: %w", job.JobID, failErr)
		}
		return nil
	}

	if err := w.store.Complete(ctx, job.JobID, fence, dispatched, skipped, failed); err != nil {
		if fenced(job.JobID, err) {
			return nil
		}
		return fmt.Errorf("failed to complete job %s: %w", job.JobID, err)
	}
	return nil
}
