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
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// admitAndDispatchDevice runs the full admission-through-publish decision
// for exactly one device within job's fan-out and records its outcome via
// RecordTask, presenting fence (the value job.JobID's owning
// Worker.HandleJobRequested call obtained from its own BeginFanOut) so a
// caller superseded by a later reclaim is rejected by the store rather
// than allowed to keep writing. It is the unwindowed path: a job with a
// forks window queues its devices instead (queueDevice) and a pump
// dispatches them (window.go).
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
	a := w.admit(job, prepared, device)
	if a.outcome != "" {
		return w.recordOutcome(ctx, job, fence, device, a.outcome, a.reason)
	}

	payload := w.dispatchPayload(ctx, job, prepared, injected, device, a)
	if reason, ok := w.publishDispatch(ctx, job, evt.TraceID, device, a.mode, payload); !ok {
		return w.recordOutcome(ctx, job, fence, device, OutcomeFailed, reason)
	}
	return w.recordOutcome(ctx, job, fence, device, OutcomeDispatched, "")
}

// queueDevice is admitAndDispatchDevice's windowed counterpart: it runs
// the same admission and records an admitted device as OutcomeQueued
// rather than dispatching it. A pump dispatches it later, when the job's
// forks window has room, and admits it again then (window.go), since a
// device's lifecycle can change while it waits.
func (w *Worker) queueDevice(ctx context.Context, job *Job, fence int64, prepared PreparedDefinition, device pkginventory.InventoryItem) (Outcome, error) {
	a := w.admit(job, prepared, device)
	if a.outcome != "" {
		return w.recordOutcome(ctx, job, fence, device, a.outcome, a.reason)
	}
	return w.recordOutcome(ctx, job, fence, device, OutcomeQueued, "")
}

// recordOutcome writes device's one JobTask row with outcome and reason
// and returns outcome, or the RecordTask error the caller must check with
// fenced and canceled.
func (w *Worker) recordOutcome(ctx context.Context, job *Job, fence int64, device pkginventory.InventoryItem, outcome Outcome, reason string) (Outcome, error) {
	if err := w.store.RecordTask(ctx, job.JobID, fence, JobTask{
		DeviceID:   string(device.ID()),
		DeviceName: device.Name(),
		Outcome:    outcome,
		Reason:     reason,
	}); err != nil {
		return "", fmt.Errorf("failed to record %s for device %s on job %s: %w", outcome, device.ID(), job.JobID, err)
	}
	return outcome, nil
}

// admission is one device's admission decision, taken before any payload
// exists: the mode and address a dispatch would use, or the outcome and
// reason that keep the device from being dispatched at all.
type admission struct {
	// mode is the job's run mode.
	mode collection.Mode
	// host is the address the Runner connects to.
	host string
	// outcome is empty when the device is admitted, and OutcomeSkipped or
	// OutcomeFailed when it is not.
	outcome Outcome
	// reason says why a device was not admitted. It names the device,
	// its lifecycle state or a capability, never a property value.
	reason string
}

// admit decides whether device may be dispatched for job: the job's mode
// must parse, the device's lifecycle must admit that mode, it must carry
// every capability the definition requires, and it must have an address.
// The fan-out and a windowed job's pump both ask, through this one
// function, so the two can never disagree about what admits a device.
func (w *Worker) admit(job *Job, prepared PreparedDefinition, device pkginventory.InventoryItem) admission {
	mode, err := job.Mode()
	if err != nil {
		return admission{outcome: OutcomeFailed, reason: err.Error()}
	}

	// A check admits a simulate-locked device, which is what such a
	// device is for, and a real run never does (engine.LifecycleAdmitsIn,
	// the one rule the CLI, validation and the engine share).
	if ok, reason := engine.LifecycleAdmitsIn(mode, device); !ok {
		return admission{mode: mode, outcome: OutcomeSkipped, reason: reason}
	}

	if ok, reason := engine.CapabilityAdmits(device, prepared.Required); !ok {
		return admission{mode: mode, outcome: OutcomeSkipped, reason: reason}
	}

	// "host", never "ip": every concrete device type in this codebase
	// populates its management address under this property key
	// (pkg/wire.DispatchPayload's own doc comment explains the "ip" bug
	// this fixes), except the generic types whose address is part of a
	// URL or a target (generic_http's base_url, generic_grpc's target),
	// which report it through their declared NetworkAddressableCapable. A
	// device with neither has nowhere for the Runner to connect to, so it
	// is skipped, not dispatched with an empty address.
	host, ok := device.Properties().String("host")
	if !ok {
		host, ok = declaredAddress(device)
	}
	if !ok {
		return admission{mode: mode, outcome: OutcomeSkipped, reason: fmt.Sprintf("device %q has no host property", device.Name())}
	}
	return admission{mode: mode, host: host}
}

// dispatchPayload builds the dispatch payload for a device admit let
// through, a's mode and host included, with the job's rendered credentials
// (injected) and, failing a bound machine credential, the device's own.
func (w *Worker) dispatchPayload(ctx context.Context, job *Job, prepared PreparedDefinition, injected credtype.Artifact, device pkginventory.InventoryItem, a admission) wire.DispatchPayload {
	host, mode := a.host, a.mode
	payload := wire.DispatchPayload{
		JobID:     job.JobID,
		RunbookID: job.RunbookID,
		// The kind travels with the dispatch so the Runner routes on a
		// value it was given rather than on a set it was compiled with.
		// Empty for a job that names no template, which the Runner
		// resolves to the native kind: the adapter such a dispatch was
		// always going to reach.
		Kind: job.Kind,
		// Carried from the job, where the launch recorded whether its
		// launcher may run it for real; the Runner reads nothing else to
		// decide whether a check may run an external program's Check.
		ExternalChecks: job.ExternalChecks,
		DeviceID:       string(device.ID()),
		DeviceName:     device.Name(),
		DeviceHost:     host,
		Interruptible:  prepared.Interruptible,
		Capabilities:   device.Capabilities(),
		Tags:           tagStrings(device.Tags()),
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
		Mode:      string(mode),
	}
	if sshCapable, ok := device.(capability.SSHTransportCapable); ok {
		payload.SSHPort = sshCapable.SSHPort()
	}
	// The device's type and its accessor properties, so the Runner rebuilds
	// the real type rather than an address-only stand-in; nothing else of
	// its record travels (record.Dispatched).
	payload.DeviceType, payload.DeviceProperties = record.Dispatched(device)

	// The job's step first, so a run that turned persistence off never
	// reads the device's hierarchy for it; then the device's own ladder,
	// through the same resolution the CLI uses. Off at either is off.
	payload.PersistConnections = launch.PersistConnections(job.Fields) && engine.PersistFor(w.repo)(ctx, device)

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
	return payload
}

// publishDispatch wraps payload in its dispatch event and publishes it on
// device's subject (the check subject for a check), under the idempotency
// key job:device. traceID continues the launch's trace when it has one.
// On failure it returns false and the reason the device's row records,
// which names the device and nothing it holds.
func (w *Worker) publishDispatch(ctx context.Context, job *Job, traceID string, device pkginventory.InventoryItem, mode collection.Mode, payload wire.DispatchPayload) (string, bool) {
	dispatchEvt, err := event.WrapPayload(uuid.New().String(), "runbook.dispatched", payload)
	if err != nil {
		return fmt.Sprintf("failed to build dispatch event for device %q", device.Name()), false
	}

	// Envelope fields mirror internal/api/dispatcher.go's own current
	// block: WithActor from the job's own stored Actor (there is no live
	// caller identity here anymore, only what was captured at launch),
	// WithTraceID from the launch's trace id when it has one (a pump has none), and
	// IdempotencyKey as jobID+":"+deviceID so a redelivery of this same
	// job.requested event, or a retry of this device's dispatch within
	// it, is recognized as a duplicate rather than double-published.
	pubCtx := event.WithActor(ctx, job.Actor)
	if traceID != "" {
		pubCtx = event.WithTraceID(pubCtx, traceID)
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
	//
	// A check goes to its own subject (topology.CheckSubject), which only a
	// Runner that knows what a check is ever consumes.
	subject := topology.DispatchSubject(string(device.ID()))
	if mode == collection.ModeCheck {
		subject = topology.CheckSubject(string(device.ID()))
	}
	if err := w.bus.Publish(pubCtx, subject, *dispatchEvt); err != nil {
		return fmt.Sprintf("failed to publish dispatch event for device %q", device.Name()), false
	}
	return "", true
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

// canceled reports whether err is (or wraps) JobStore's ErrCanceled,
// logging an Info-level line naming jobID when it is. A true result means
// somebody stopped this job while its fan-out was in flight, and the
// caller must stop and return nil from HandleJobRequested exactly as it
// does for fenced: retrying cannot help, because a cancel never becomes
// un-canceled.
//
// Separate from fenced rather than folded into it, even though both mean
// "stop and ack", because the two log lines are the whole point. Fenced
// says another worker owns this job now; canceled says nobody should be
// running it at all. Collapsing them would print the wrong cause for
// whichever one the shared message did not name, and the log line is
// exactly what somebody reads when asking why a fan-out ended early.
func canceled(jobID string, err error) bool {
	if !errors.Is(err, ErrCanceled) {
		return false
	}
	slog.Info("job fan-out stopped: the job was canceled",
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

// declaredAddress returns the address a device declares through
// NetworkAddressableCapable, when it declares one.
func declaredAddress(device pkginventory.InventoryItem) (string, bool) {
	addressable, ok := device.(capability.NetworkAddressableCapable)
	if !ok || !device.HasCapability(capability.NameNetworkAddressable) || addressable.IPAddress() == "" {
		return "", false
	}
	return addressable.IPAddress(), true
}
