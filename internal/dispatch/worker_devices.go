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

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
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
//
// injected is this job's credential artifact, already rendered once for the
// whole fan-out by the caller (see HandleJobRequested and inject.go for why
// it is rendered there rather than here). It is the same value for every
// device, and it is passed rather than recomputed so a ten-thousand-device
// fan-out renders its credentials once.
func (w *Worker) admitAndDispatchDevice(ctx context.Context, job *Job, fence int64, prepared PreparedDefinition, injected credtype.Artifact, evt event.Event, device pkginventory.InventoryItem) (Outcome, error) {
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

	if ok, reason := engine.CapabilityAdmits(device, prepared.Required); !ok {
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
		JobID:     job.JobID,
		RunbookID: job.RunbookID,
		// The kind travels with the dispatch so the Runner routes on a
		// value it was given rather than on a set it was compiled with.
		// Empty for a job that names no template, which the Runner
		// resolves to the native kind: the adapter such a dispatch was
		// always going to reach.
		Kind:          job.Kind,
		DeviceID:      string(device.ID()),
		DeviceName:    device.Name(),
		DeviceHost:    host,
		Interruptible: prepared.Interruptible,
		Capabilities:  device.Capabilities(),
		Tags:          tagStrings(device.Tags()),
		// Fields and ExtraVars are job's own resolved launch.Fields/
		// ExtraVars (AWX_PARITY_ROADMAP.md Section 3b.1's second wire hop:
		// the first hop stamped them onto job itself, at LaunchTemplate).
		// map[string]any(job.Fields) is a bare conversion, not a copy:
		// launch.Fields and wire.DispatchPayload.Fields share an identical
		// underlying type (pkg/wire must never import internal/launch), so
		// this is the same value, differently named on either side of that
		// boundary. Every device dispatched from this job's fan-out
		// carries an identical copy: these are launch-time values, not
		// per-device ones.
		Fields:    map[string]any(job.Fields),
		ExtraVars: job.ExtraVars,
	}
	if sshCapable, ok := device.(capability.SSHTransportCapable); ok {
		payload.SSHPort = sshCapable.SSHPort()
	}

	// The template's own bound credentials, already rendered for the whole
	// fan-out, reach the payload first. A machine credential among them
	// supplies authentication for EVERY device in this dispatch, which is
	// AWX's semantics: an operator binds one machine credential to a job
	// template and every host in the inventory is reached with it.
	//
	// applyInjection (inject.go) is where that precedence lives, and its
	// doc comment carries the reasoning for the ordering, including why the
	// inverse would be worse.
	applyInjection(&payload, injected)

	// The per-device store is the FALLBACK, consulted only when the
	// template bound no machine credential. It is what keeps every dispatch
	// that exists today working unchanged, including the whole Crawl tier,
	// which has no template and no binding.
	//
	// The Controller resolves this device's credential now, at fan-out
	// time, and attaches it directly to the payload (PLAN.md Section 17's
	// Just-in-Time delivery principle, per Phase 16's own design
	// decision): the Runner never holds its own copy of the decryption
	// key. A missing credential (ErrNotFound) is not a dispatch failure --
	// only a task that actually needs a secret fails downstream, the same
	// place a missing credential already fails at the Crawl tier. Any
	// other error (a real store failure: a corrupt file, a bad master
	// key) is logged and the device proceeds with no secrets rather than
	// being skipped outright, since a device that only runs
	// capability-free tasks should not fail merely because credential
	// storage itself is unhealthy.
	if payload.Secrets == nil && w.credentials != nil {
		cred, err := w.credentials.Lookup(ctx, device.Name())
		switch {
		case err == nil:
			payload.Secrets = credential.Flatten(cred)
		case errors.Is(err, credential.ErrNotFound):
			// No credential stored for this device; Secrets stays empty.
		default:
			slog.Warn("credential lookup failed while dispatching device; proceeding with no secrets",
				slog.String("job_id", job.JobID),
				slog.String("device_name", device.Name()),
				slog.Any("error", err))
		}
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
	// The device's own id is what scopes this subject, and it is the same
	// value the idempotency key above is built from rather than a second
	// spelling of it.
	if err := w.bus.Publish(pubCtx, topology.DispatchSubject(string(device.ID())), *dispatchEvt); err != nil {
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

// tagStrings converts device.Tags()'s own []pkginventory.Tag into the
// []string wire.DispatchPayload.Tags carries. A bare slice conversion
// ([]string)(tags) is not legal Go here: pkginventory.Tag and string are
// distinct named types with the same underlying type, and the language's
// slice-conversion rule requires identical (not merely convertible)
// element types.
func tagStrings(tags []pkginventory.Tag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = string(t)
	}
	return out
}
