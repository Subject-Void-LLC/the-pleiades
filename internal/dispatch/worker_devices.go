// Package dispatch: the per-device admission-and-publish block
// Worker.HandleJobRequested's own device loop (worker.go) calls once per
// device. Split into its own sibling file, following job.go/ent_store.go's
// existing file-per-concern shape, once worker.go's single
// HandleJobRequested function grew past AGENTS.md's ~300-line soft cap on
// logic files: this is the single largest coherent, self-contained piece
// of that function, admitting or skipping one device and, if admitted,
// building and publishing its dispatch event, all behind one RecordTask
// write recording exactly one outcome.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// admitAndDispatchDevice runs the full admission-through-publish decision
// for exactly one device within job's fan-out and records its outcome via
// RecordTask, presenting fence (the value job.JobID's owning
// Worker.HandleJobRequested call obtained from its own BeginFanOut) so a
// caller superseded by a later reclaim is rejected by the store rather
// than allowed to keep writing.
//
// It returns the Outcome actually recorded (OutcomeSkipped, OutcomeFailed,
// or OutcomeDispatched) and a nil error on every path that successfully
// wrote a JobTask row, matching HandleJobRequested's own device-loop
// tallying (dispatched/skipped/failed) one for one. It returns a non-nil
// error only when the RecordTask write itself failed, which the caller
// must check with the fenced helper (worker.go) before treating it as
// fatal: a wrapped ErrFenced means this call's own claim was superseded
// mid-device and the caller must stop the whole loop immediately, not
// merely skip this one device.
func (w *Worker) admitAndDispatchDevice(ctx context.Context, job *Job, fence int64, rb *runbook.Runbook, evt event.Event, device pkginventory.InventoryItem) (Outcome, error) {
	if ok, reason := engine.LifecycleAdmits(device); !ok {
		if err := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
			DeviceID:   string(device.ID()),
			DeviceName: device.Name(),
			Outcome:    OutcomeSkipped,
			Reason:     reason,
		}); err != nil {
			return "", fmt.Errorf("failed to record skip for device %s on job %s: %w", device.ID(), job.JobID, err)
		}
		return OutcomeSkipped, nil
	}

	if ok, reason := engine.CapabilityAdmits(device, rb.Required); !ok {
		if err := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
			DeviceID:   string(device.ID()),
			DeviceName: device.Name(),
			Outcome:    OutcomeSkipped,
			Reason:     reason,
		}); err != nil {
			return "", fmt.Errorf("failed to record skip for device %s on job %s: %w", device.ID(), job.JobID, err)
		}
		return OutcomeSkipped, nil
	}

	// "host", never "ip": every concrete device type in this codebase
	// populates its management address under this property key
	// (pkg/wire.DispatchPayload's own doc comment explains the "ip" bug
	// this fixes). A device with no "host" property has nowhere for the
	// Runner to connect to, so it is skipped, not dispatched with an
	// empty address.
	host, ok := device.Properties().String("host")
	if !ok {
		reason := fmt.Sprintf("device %q has no host property", device.Name())
		if err := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
			DeviceID:   string(device.ID()),
			DeviceName: device.Name(),
			Outcome:    OutcomeSkipped,
			Reason:     reason,
		}); err != nil {
			return "", fmt.Errorf("failed to record skip for device %s on job %s: %w", device.ID(), job.JobID, err)
		}
		return OutcomeSkipped, nil
	}

	payload := wire.DispatchPayload{
		JobID:         job.JobID,
		RunbookID:     job.RunbookID,
		DeviceID:      string(device.ID()),
		DeviceName:    device.Name(),
		DeviceHost:    host,
		Interruptible: rb.Interruptible,
	}

	dispatchEvt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", payload)
	if err != nil {
		if recErr := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
			DeviceID:   string(device.ID()),
			DeviceName: device.Name(),
			Outcome:    OutcomeFailed,
			Reason:     fmt.Sprintf("failed to build dispatch event for device %q", device.Name()),
		}); recErr != nil {
			return "", fmt.Errorf("failed to record failure for device %s on job %s: %w", device.ID(), job.JobID, recErr)
		}
		return OutcomeFailed, nil
	}

	// Envelope fields mirror internal/api/dispatcher.go's own current
	// block: WithActor from the job's own stored Actor (there is no live
	// caller identity here anymore, only what was captured at launch),
	// WithTraceID from evt's own TraceID when it carries one, and
	// IdempotencyKey as jobID+":"+deviceID so a redelivery of this same
	// job.requested event, or a retry of this device's dispatch within
	// it, is recognized as a duplicate rather than double-published.
	pubCtx := event.WithActor(ctx, job.Actor)
	if evt.TraceID != "" {
		pubCtx = event.WithTraceID(pubCtx, evt.TraceID)
	}
	pubCtx = event.WithIdempotencyKey(pubCtx, job.JobID+":"+string(device.ID()))

	// Deliberately no live OpenTelemetry span is grafted onto this
	// publish, unlike internal/api/dispatcher.go's own block (which can
	// pull trace.SpanFromContext(r.Context()) from a real HTTP request).
	// event.Bus.Subscribe's handler signature (func(Event) error) exposes
	// only the decoded Event, never the transport's own raw message or
	// headers the way internal/runner/agent.go's separate, hand-rolled
	// pull loop can reach into. Recovering a live span here would require
	// reaching into a concrete NATS type from inside this handler, which
	// would both violate this package's own port boundary (Worker
	// depends on event.Bus, an interface, never a driver) and fail
	// internal/archtest's driver-allowlist test (internal/dispatch is
	// not on that allowlist, deliberately: it has no business opening a
	// NATS connection of its own). So full span continuity across this
	// seam is not achievable without that violation, and this comment
	// documents that as a known, accepted limitation rather than an
	// oversight.
	if err := w.bus.Publish(pubCtx, topology.DispatchSubject(), *dispatchEvt); err != nil {
		if recErr := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
			DeviceID:   string(device.ID()),
			DeviceName: device.Name(),
			Outcome:    OutcomeFailed,
			Reason:     fmt.Sprintf("failed to publish dispatch event for device %q", device.Name()),
		}); recErr != nil {
			return "", fmt.Errorf("failed to record failure for device %s on job %s: %w", device.ID(), job.JobID, recErr)
		}
		return OutcomeFailed, nil
	}

	if err := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
		DeviceID:   string(device.ID()),
		DeviceName: device.Name(),
		Outcome:    OutcomeDispatched,
	}); err != nil {
		return "", fmt.Errorf("failed to record dispatch for device %s on job %s: %w", device.ID(), job.JobID, err)
	}
	return OutcomeDispatched, nil
}

// fenced reports whether err is (or wraps) JobStore's ErrFenced, logging
// an Info-level line naming jobID when it is. A true result means this
// Worker's claim on jobID's fan-out was superseded by a later BeginFanOut
// reclaim (see JobStore.BeginFanOut's own doc comment): every caller of
// this helper, across both this file and worker.go, is expected to stop
// immediately and return nil from HandleJobRequested (acking the
// delivery) the moment it sees true, rather than treating the write
// failure as a fatal error worth a redelivery-triggered retry. Retrying
// can never help here: a stale fence never becomes current again, only a
// fresh BeginFanOut call could obtain one, and this invocation is not
// going to make one.
func fenced(jobID string, err error) bool {
	if !errors.Is(err, ErrFenced) {
		return false
	}
	slog.Info("job fan-out stopped: ownership was reclaimed by another worker",
		slog.String("job_id", jobID))
	return true
}
