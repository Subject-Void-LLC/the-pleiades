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
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
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

	// Prompted credential inputs live on this Worker for exactly the span
	// of this call, and the deferred forget runs on every exit path,
	// including the ones that return before injection: a plaintext value
	// held past the fan-out that needed it is a plaintext value held for no
	// reason.
	w.rememberPrompted(payload.JobID, payload.Prompted)
	defer w.forgetPrompted(payload.JobID)

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

	// The definition is prepared through the source its KIND owns, looked
	// up in the same map shape the Runner's adapter routing uses, so no
	// consumer branches on kind. Resolving every job through the runbook
	// source regardless, which is what this block used to do, reported
	// every playbook job as "runbook not found" inside the Controller
	// before the Runner's adapter selection was ever consulted.
	kind := launch.ResolveKind(job.Kind)
	source, ok := w.definitions[kind]
	if !ok {
		// A real, permanent failure case: a kind this Controller has no
		// definition source for cannot become preparable by redelivery,
		// and BeginFanOut above already made this call the job's only
		// chance to reach a terminal state (see the retry reasoning on
		// the resolution failure below).
		reason := fmt.Sprintf("this controller has no definition source for %q jobs", kind)
		slog.Error("job fan-out has no definition source for the job's kind",
			slog.String("job_id", job.JobID),
			slog.String("kind", kind))
		if failErr := w.store.Fail(ctx, job.JobID, fence, reason); failErr != nil {
			if fenced(job.JobID, failErr) {
				return nil
			}
			return fmt.Errorf("failed to record job %s as failed: %w", job.JobID, failErr)
		}
		return nil
	}

	prepared, reason, err := source.Prepare(ctx, job.RunbookID)
	if err != nil {
		// This is a real, permanent failure case the state enum could not
		// honestly represent before internal/ent/schema/job.go added a
		// "failed" state alongside "completed": a definition that cannot
		// be resolved means fan-out never even considered a single
		// device, which "completed" with all-zero tallies would misreport
		// as indistinguishable from a legitimate dispatch against an
		// empty inventory. See that schema's own State field comment.
		//
		// reason is the source's own sanitised sentence (the split
		// DefinitionSource documents): the job record carries it, and
		// err's full text is logged server-side only, the same posture
		// internal/api/dispatcher.go takes with the repository's error
		// text.
		slog.Error("job fan-out failed to resolve the job's definition",
			slog.String("job_id", job.JobID),
			slog.String("kind", kind),
			slog.String("definition", job.RunbookID),
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

	// Credentials are resolved and rendered ONCE per job, here, before any
	// device is considered, rather than once per device inside the loop.
	// They are launch-time values: every device in a fan-out receives the
	// identical artifact, so rendering per device would repeat the same
	// work ten thousand times and register the same secrets ten thousand
	// times with the masking set.
	//
	// A failure fails the whole job rather than recording a per-device
	// outcome, and that is the honest shape: a credential that cannot be
	// resolved or rendered is not a property of any one device, and writing
	// ten thousand identical JobTask rows saying so would bury the one
	// sentence an operator needs. It is the same treatment source.Prepare's
	// failure above already gets, for the same reason.
	injected, reason, err := w.injectFor(ctx, job)
	if err != nil {
		// The full error is logged server-side; reason is the sanitised
		// sentence the job record carries. Neither ever contains a
		// credential value: internal/credtype's own messages are written to
		// that rule, and resolve's are too.
		slog.Error("job fan-out could not inject its credentials",
			slog.String("job_id", job.JobID),
			slog.String("error", err.Error()))
		if failErr := w.store.Fail(ctx, job.JobID, fence, reason); failErr != nil {
			if fenced(job.JobID, failErr) {
				return nil
			}
			return fmt.Errorf("failed to record job %s as failed: %w", job.JobID, failErr)
		}
		return nil
	}

	selector, reason, err := w.targetSelector(ctx, job)
	if err != nil {
		// The repository's own error text is logged server-side only and
		// never surfaced onto the job resource, mirroring
		// internal/api/dispatcher.go's posture: it can name tables,
		// columns and hosts. reason is the sanitised sentence a caller
		// polling the job actually reads.
		slog.Error("job fan-out could not resolve its targets",
			slog.String("job_id", job.JobID),
			slog.Int("inventory_id", job.InventoryID),
			slog.String("error", err.Error()))
		if failErr := w.store.Fail(ctx, job.JobID, fence, reason); failErr != nil {
			if fenced(job.JobID, failErr) {
				return nil
			}
			return fmt.Errorf("failed to record job %s as failed: %w", job.JobID, failErr)
		}
		return nil
	}

	iter, err := w.repo.GetGroup(ctx, selector)
	if err != nil {
		slog.Error("job fan-out failed to query inventory",
			slog.String("job_id", job.JobID),
			slog.Int("inventory_id", job.InventoryID),
			slog.String("error", err.Error()))
		if failErr := w.store.Fail(ctx, job.JobID, fence, "failed to query the devices this job targets"); failErr != nil {
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
		outcome, err := w.admitAndDispatchDevice(ctx, job, fence, prepared, injected, evt, device)
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

// targetSelector resolves what a job dispatches against.
//
// It returns the selector, and on failure a sanitised reason the job record
// can carry. Two return values rather than one error because the two
// audiences are different: the error is for the operator reading logs and
// may name storage internals, the reason is for whoever is polling the job
// and must not.
//
// A job naming an inventory streams that inventory's membership: its groups
// plus the devices attached to it directly, as one query the database
// de-duplicates. A job naming none is a pre-Phase-21 record, and streams by
// group name exactly as it always did.
//
// Three refusals, and each exists because the alternative is worse than an
// error:
//
//   - No set store wired. A Worker built without one cannot resolve an
//     inventory, and falling through to an unrestricted selector would
//     dispatch to every device the platform manages.
//   - The inventory is gone. Somebody deleted the set a template names;
//     the job says so rather than running against nothing or everything.
//   - The inventory is empty. A fan-out that reaches zero devices is
//     indistinguishable from one that failed, so the job says which it was.
func (w *Worker) targetSelector(ctx context.Context, job *Job) (pkginventory.Selector, string, error) {
	if job.InventoryID <= 0 {
		return pkginventory.Selector{GroupName: job.GroupName}, "", nil
	}

	if w.sets == nil {
		return pkginventory.Selector{}, "this controller cannot resolve the inventory this job targets",
			fmt.Errorf("worker has no inventory set store, so job %s cannot be targeted", job.JobID)
	}

	set, err := w.sets.Get(ctx, job.InventoryID)
	if err != nil {
		if errors.Is(err, inventory.ErrSetNotFound) {
			return pkginventory.Selector{},
				fmt.Sprintf("inventory %d no longer exists", job.InventoryID), err
		}
		return pkginventory.Selector{},
			fmt.Sprintf("failed to resolve inventory %d", job.InventoryID), err
	}

	if set.Empty() {
		return pkginventory.Selector{},
			fmt.Sprintf("inventory %d contains no devices", job.InventoryID),
			fmt.Errorf("inventory %d is empty", job.InventoryID)
	}

	return set.Selector(), "", nil
}
