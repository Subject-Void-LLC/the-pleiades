// Package runner: Agent's per-device lock lease, heartbeat, and
// self-abort supervisor, split into its own sibling file per this
// repository's own file-per-concern convention (internal/dispatch's
// worker.go/worker_devices.go/worker_config.go split).
//
// This closes two related PLAN.md commitments together, since they share
// one mechanism: Section 13's "Locks live in the backend... a device can
// only have one exclusive execution running against it at a time," and
// Section 16's Network Partitions mitigation, "If a Runner loses
// heartbeat with the Controller, it self-aborts execution before the
// Controller TTL expires... Exception: Un-abortable tasks
// (interruptible: false) finish execution, and the Controller quarantines
// the device instead of re-issuing the lock."
//
// Section 16 was written assuming a persistent Runner<->Controller
// connection (gRPC, its own stated primary communication strategy) whose
// drop is the literal "lost heartbeat." This codebase's actual,
// deliberately-chosen shape is NATS-pull-only (PATTERNS.md's own "Push
// vs. Pull Execution Model" entry documents this as intentional): there
// is no direct Runner<->Controller connection to lose. The concrete,
// already-built signal that plays the same role is this Agent's own
// lock.Lease.KeepAlive against the shared distributed lock store PLAN.md
// Section 16.1 itself already names as the Controller's own Lock Manager
// component. executeWithLease/heartbeat below read "lost heartbeat with
// the Controller" as "this Agent's KeepAlive calls against its held
// device lease are failing" -- the only concrete signal this NATS-pull
// architecture actually has.
package runner

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

const (
	// execLeaseTTL bounds how long Agent holds a device's lock lease
	// before lock.Manager treats it abandoned if this process dies
	// without releasing it, mirroring engine.defaultLockTTL's own role
	// exactly (internal/engine/executor.go). Five minutes is a
	// placeholder sized for how long a runbook might realistically run,
	// not a measured figure: native.Adapter.Execute today takes about a
	// second (it is still simulated, Phase 16's own open item), so this
	// value has nothing real to be tuned against yet. Overridable via
	// WithLeaseTTL.
	execLeaseTTL = 5 * time.Minute

	// heartbeatInterval is how often heartbeat calls lease.KeepAlive
	// while an execution is in flight, mirroring
	// election.electionInterval's own roughly-1:4 ratio to its own lease
	// TTL (election.go) so a single missed tick is never mistaken for a
	// genuine, sustained heartbeat loss. Overridable via
	// WithHeartbeatInterval.
	heartbeatInterval = 1 * time.Minute
)

// WithCancelSignals lets an execution be stopped by an operator cancelling
// its job, by subscribing to that job's control subject for exactly as long
// as it is running (see executeWithLease).
//
// Optional, and omitting it is not a broken Runner: the durable half of a
// cancel, the job's record and every device its fan-out has not yet
// reached, is settled entirely by the Controller and does not involve this
// at all. What an Agent without it cannot do is stop work already running
// on a device, which will instead finish and report normally.
func WithCancelSignals(control event.CancelSubscriber) AgentOption {
	return func(a *Agent) {
		a.control = control
	}
}

// executeWithLease acquires an exclusive lock.Manager lease on
// payload.DeviceID (PLAN.md Section 13: "a device can only have one
// exclusive execution running against it at a time"), runs a heartbeat
// loop against that lease for the duration of a.adapter.Execute
// (mirroring election.LeaderElector.Run's own ticker-plus-KeepAlive
// shape; PATTERNS.md's Heartbeat entry already cites that method as this
// codebase's precedent), and self-aborts (cancels the context Execute was
// given) the moment a heartbeat fails -- unless payload.Interruptible is
// false, in which case Execute is left to run to completion (PLAN.md
// Section 16's own named exception).
//
// execCtx is deliberately NOT a direct child of ctx (contrast: the lease
// Acquire call above still uses ctx itself, since an in-progress acquire
// attempt should still respect the caller's own cancellation). ctx is
// Agent.Run's own outer context, which a graceful shutdown (SIGTERM/
// SIGINT, cmd/runner/main.go) cancels; if execCtx inherited that
// cancellation unconditionally, a non-interruptible payload would abort
// on an ordinary Runner restart exactly as if it were interruptible,
// silently defeating PLAN.md Section 16's own named exception ("Un-
// abortable tasks... finish execution") for the one trigger (a process
// shutdown, not a lease-heartbeat failure) that exception exists to
// survive. Instead, execCtx is built over a value-only view of ctx
// (detachedValueContext, below), which still carries ctx's own values
// (the active OpenTelemetry span, in particular -- handleMessage starts
// it before calling this method) but never its cancellation or deadline;
// a watcher goroutine then propagates ctx's own cancellation into execCtx
// only when payload.Interruptible allows it. heartbeat's own KeepAlive-
// failure branch already gates its own cancelExec() call on interruptible
// identically, so both triggers now honor the flag consistently.
//
// It returns Execute's own result (possibly context.Canceled, on a
// self-abort), errLockContention wrapping the lock.Manager error if the
// lease could not be acquired at all (a distinct outcome the caller,
// handleMessage, must not treat as an execution failure, since Execute
// was never actually invoked), or an error describing a panic recovered
// from a.adapter.Execute (see the deferred recover below): a future real
// adapter that panics (a nil dereference, an out-of-range index, a failed
// type assertion against unexpected transport output) must not crash this
// whole Runner process and every other concurrently in-flight worker
// along with it, mirroring internal/event/consumer.go's own handleDelivery
// panic-recovery precedent for a handler this codebase does not control.
func (a *Agent) executeWithLease(ctx context.Context, payload wire.DispatchPayload) (execErr error) {
	lease, err := a.locks.Acquire(ctx, payload.DeviceID, a.leaseTTL, lock.AcquireOptions{})
	if err != nil {
		return fmt.Errorf("%w: %w", errLockContention, err)
	}
	defer func() {
		// ctx (and therefore execCtx below) may already be canceled by a
		// self-abort by the time we get here; Release still needs to run
		// so the lock is not held until its TTL expires, so it goes
		// against a fresh background context, exactly the way
		// engine.run.runOne's own identical defer already does.
		_ = lease.Release(context.Background())
	}()

	execCtx, cancelExec := context.WithCancel(detachedValueContext{parent: ctx})
	if payload.Interruptible {
		go func() {
			select {
			case <-ctx.Done():
				cancelExec()
			case <-execCtx.Done():
			}
		}()

		// The third trigger: an operator cancelled this job. Gated on the
		// same payload.Interruptible flag as the outer-shutdown watcher
		// above and heartbeat's own KeepAlive-failure branch below, so all
		// three honour PLAN.md Section 16's exception consistently. A task
		// declaring itself un-abortable finishes whatever asks it to stop,
		// and the Controller's own record still says canceled: the record
		// is what a person decided, and this signal is only how far that
		// decision can reach into work already running.
		//
		// Subscribing here rather than in handleMessage keeps it bounded
		// by exactly the window where cancelling means anything: from just
		// before Execute begins to just after it returns. A subscription
		// held any longer would be a Runner listening for the cancellation
		// of a job it is no longer running.
		if a.control != nil {
			unsubscribe, err := a.control.SubscribeCancel(ctx, payload.JobID, cancelExec)
			if err != nil {
				// Logged, never fatal. A Runner that cannot hear
				// cancellations still executes correctly, and refusing the
				// dispatch would turn a degraded best-effort channel into
				// a refusal to do any work at all.
				a.logger.Warn("could not subscribe to cancel signals for this job; it will run to completion if canceled",
					slog.String("job_id", payload.JobID), slog.String("error", err.Error()))
			} else {
				defer unsubscribe()
			}
		}
	}

	done := make(chan struct{})
	go a.heartbeat(execCtx, lease, cancelExec, payload.Interruptible, done)

	// Runs on every exit from this point on, normal return and a panic
	// unwind alike, so the heartbeat goroutine is always fully stopped
	// (and therefore never still touching lease) before the deferred
	// lease.Release above runs: a panic skips ordinary statements placed
	// after the call that panicked, but never skips an already-registered
	// defer, which is why this cleanup lives here rather than as
	// unconditional code following the Execute call below.
	defer func() {
		cancelExec()
		<-done
		if r := recover(); r != nil {
			a.logger.Error("adapter execution panicked", slog.String("device_id", payload.DeviceID), slog.Any("panic", r))
			execErr = fmt.Errorf("adapter execution panicked: %v", r)
		}
	}()

	return a.adapter.Execute(execCtx, payload)
}

// detachedValueContext wraps parent, exposing its Value lookups (so a
// child built from this context still carries e.g. an active
// OpenTelemetry span) while reporting itself as never canceled and having
// no deadline. context.WithCancel(detachedValueContext{parent}) therefore
// produces a context whose own cancellation is controlled exclusively by
// its own cancel func, never automatically by parent's -- the seam
// executeWithLease needs to let a non-interruptible execution outlive
// Agent.Run's own shutdown signal while still propagating it deliberately
// when payload.Interruptible allows that.
type detachedValueContext struct {
	parent context.Context
}

// Deadline implements context.Context, always reporting no deadline.
func (d detachedValueContext) Deadline() (time.Time, bool) { return time.Time{}, false }

// Done implements context.Context, returning nil (a channel that never
// fires): this context is never canceled by anything other than a
// context.WithCancel built on top of it calling its own cancel func.
func (d detachedValueContext) Done() <-chan struct{} { return nil }

// Err implements context.Context, always reporting no error, matching
// Done's own "never canceled" contract.
func (d detachedValueContext) Err() error { return nil }

// Value implements context.Context by delegating to parent, the one
// thing this wrapper exists to still carry through.
func (d detachedValueContext) Value(key any) any { return d.parent.Value(key) }

// heartbeat renews lease every a.heartbeatEvery until execCtx is done. On
// a renewal failure, this Agent has lost its own proof of still holding
// deviceID's lock (see this file's own doc comment for why that is this
// codebase's reading of PLAN.md Section 16's "lost heartbeat with the
// Controller"): if interruptible, cancel (self-abort, stopping short of
// the Controller's own TTL expiry rather than racing it); if not, log and
// keep trying on the next tick, honoring interruptible: false's
// documented exception. The Controller-side "quarantine the device"
// reaction that exception names has no consumer anywhere in this
// codebase yet and is deliberately not built here -- a future phase's
// work, once a real execution (Phase 16) exists for the Controller to
// quarantine a device away from.
func (a *Agent) heartbeat(execCtx context.Context, lease lock.Lease, cancel context.CancelFunc, interruptible bool, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(a.heartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-execCtx.Done():
			return
		case <-ticker.C:
			if err := lease.KeepAlive(execCtx); err != nil {
				if execCtx.Err() != nil {
					// Execute already finished (or was itself canceled)
					// by the time this KeepAlive call returned; ordinary
					// shutdown race, not a real heartbeat loss.
					return
				}
				if interruptible {
					a.logger.Warn("lost device lease heartbeat, self-aborting execution",
						slog.String("device_id", lease.ID()), slog.String("error", err.Error()))
					cancel()
					return
				}
				a.logger.Error("lost device lease heartbeat but task is not interruptible, continuing to completion",
					slog.String("device_id", lease.ID()), slog.String("error", err.Error()))
				// Deliberately does not cancel and does not return: this
				// loop keeps trying to reacquire the heartbeat on the
				// next tick in case the outage clears before Execute
				// finishes on its own.
			}
		}
	}
}
