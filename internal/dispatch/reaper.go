// Package dispatch: Reaper, the periodic sweep that gives
// JobStore.BeginFanOut's staleAfter reclaim a trigger that does not depend
// on NATS ever redelivering a job.requested message.
package dispatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/google/uuid"
)

// defaultReapInterval is how often a Reaper scans for stale fan-outs while
// it holds leadership. It must stay comfortably below staleAfter so a job
// crossing that threshold is not left un-reclaimed for long, and cheap
// enough to run indefinitely: each tick is one indexed query
// (JobStore.ListStaleFanOuts) plus, on the normal case of zero stale jobs
// found, nothing else.
const defaultReapInterval = time.Minute

// Reaper periodically finds jobs stranded in "fanning_out" past their own
// staleAfter window and re-publishes job.requested for each one, giving
// Worker.HandleJobRequested's own BeginFanOut reclaim (already correct, see
// JobStore.BeginFanOut's own doc comment) a way to actually run. See
// JobStore.ListStaleFanOuts's own doc comment for why this exists at all:
// JetStream's redelivery budget is far shorter than any realistic
// staleAfter, so natural redelivery alone never reaches the reclaim
// branch.
//
// A Reaper does none of the fan-out work itself. Re-publishing is the
// entire mechanism: Worker.HandleJobRequested, BeginFanOut, and every
// per-device admission/dispatch decision are reused completely unchanged,
// so this package's one fan-out implementation stays the only one.
type Reaper struct {
	store      JobStore
	bus        event.Bus
	staleAfter time.Duration
	interval   time.Duration

	// pump, when set, is called for every running job that still has
	// queued devices, catching a windowed job whose pump after a result
	// never ran (a replica that crashed between the two). Nil pumps
	// nothing.
	pump func(ctx context.Context, jobID string) error
}

// ReaperOption configures optional, non-default behavior on a Reaper built
// by NewReaper.
type ReaperOption func(*Reaper)

// WithReapInterval overrides a Reaper's sweep interval from its production
// default (defaultReapInterval). Exists chiefly for tests that need to
// prove sweep behavior without waiting out the real interval.
func WithReapInterval(interval time.Duration) ReaperOption {
	return func(r *Reaper) {
		r.interval = interval
	}
}

// WithReaperPump has each sweep call pump (Worker.Pump) for every running
// job that still has queued devices.
func WithReaperPump(pump func(ctx context.Context, jobID string) error) ReaperOption {
	return func(r *Reaper) {
		r.pump = pump
	}
}

// NewReaper builds a Reaper over store and bus. staleAfter must be the
// identical value the Worker(s) consuming job.requested for these jobs use
// as their own fanOutLeaseTTL (see JobStore.ListStaleFanOuts's own doc
// comment for why the two must agree): production wiring passes the same
// constant to both NewWorker and NewReaper rather than letting either
// default independently.
func NewReaper(store JobStore, bus event.Bus, staleAfter time.Duration, opts ...ReaperOption) *Reaper {
	r := &Reaper{
		store:      store,
		bus:        bus,
		staleAfter: staleAfter,
		interval:   defaultReapInterval,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Run blocks until ctx is done, ticking every r.interval and sweeping for
// stale fan-outs only while isLeader reports true. isLeader is a plain
// function rather than an internal/election.LeaderElector so this package
// need not import election at all; a caller passes elector.IsLeader
// directly. Exactly one running replica should ever observe isLeader true
// at a time (that guarantee is internal/election's own, not this
// package's), so at most one Reaper is ever actively sweeping across a
// whole deployment, keeping ListStaleFanOuts's own query load bounded
// regardless of replica count.
//
// A tick where isLeader is false, or where zero stale jobs are found, does
// nothing beyond the one query: this is the expected, overwhelmingly
// common case, not a degraded one.
func (r *Reaper) Run(ctx context.Context, isLeader func() bool) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !isLeader() {
				continue
			}
			r.sweep(ctx)
		}
	}
}

// sweep runs one scan-and-republish pass. A failure listing stale jobs, or
// publishing for one specific job, is logged and does not stop the rest of
// this tick or any future one: the next tick's own ListStaleFanOuts call
// will find the same still-stale job again, so a transient failure here
// costs at most one reap interval of extra delay, never a lost job.
func (r *Reaper) sweep(ctx context.Context) {
	staleIDs, err := r.store.ListStaleFanOuts(ctx, r.staleAfter)
	if err != nil {
		slog.Error("reaper failed to list stale fan-outs", slog.String("error", err.Error()))
		return
	}

	for _, jobID := range staleIDs {
		if err := r.republish(ctx, jobID); err != nil {
			slog.Error("reaper failed to republish job.requested for stale fan-out",
				slog.String("job_id", jobID),
				slog.String("error", err.Error()))
			continue
		}
		slog.Info("reaper republished job.requested for a stale fan-out",
			slog.String("job_id", jobID))
	}
	r.pumpQueued(ctx)
}

// pumpQueued pumps every running job that still has queued devices. It is
// the backstop for the pump a result should have triggered: pumping a job
// whose window is already full, or that another replica is pumping right
// now, does nothing, so a sweep can never start more than forks devices.
func (r *Reaper) pumpQueued(ctx context.Context) {
	if r.pump == nil {
		return
	}
	jobIDs, err := r.store.ListQueuedJobs(ctx)
	if err != nil {
		slog.Error("reaper failed to list jobs with queued devices", slog.String("error", err.Error()))
		return
	}
	for _, jobID := range jobIDs {
		if err := r.pump(ctx, jobID); err != nil {
			slog.Error("reaper failed to pump a windowed job",
				slog.String("job_id", jobID),
				slog.String("error", err.Error()))
		}
	}
}

// republish builds and publishes a fresh job.requested event for jobID,
// the identical wire shape and idempotency-key convention
// internal/api/dispatcher.go's own original publish uses ("the job id
// alone is a natural, stable idempotency key for a retry of this exact
// publish"), so BeginFanOut's staleAfter reclaim, once this delivers, sees
// exactly the message shape it already handles.
//
// This is safe against JetStream's own producer-side duplicate
// suppression precisely because it is not a retry within that window:
// internal/topology's duplicate window is derived from the deployment's
// outage budget but CAPPED at half the ten minute staleAfter this call
// waits for, precisely so that this reasoning keeps holding as the budget
// grows: the original publish's dedup window has always closed by the
// time a reap tick republishes. topology.DerivedDuplicateWindow carries
// the cap, and a test asserts it rather than trusting this comment.
func (r *Reaper) republish(ctx context.Context, jobID string) error {
	evt, err := event.WrapPayload(uuid.New().String(), "job.requested", jobRequestedPayload{JobID: jobID})
	if err != nil {
		return err
	}
	pubCtx := event.WithIdempotencyKey(ctx, jobID)
	return r.bus.Publish(pubCtx, topology.JobRequestedSubject(), *evt)
}
