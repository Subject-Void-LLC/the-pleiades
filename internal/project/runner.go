// This file is the asynchronous sync engine: the thing that lets pressing
// Sync return before a clone finishes.
//
// Before it, a sync ran inside the request that asked for it, so a slow
// clone held a page open for as long as the network took. The Runner moves
// the clone onto a bounded background pool with its own context, records
// the outcome when it lands, and leaves the request free to redirect to a
// page whose Refresh badge settles on its own.
//
// It owns none of the durability the store provides: a claim is a
// compare-and-swap in the database (Store.BeginSync), and a claim a crash
// stranded is cleared at the next startup (RecoverInterrupted). What the
// Runner adds is the concurrency, the lifecycle, and the promise that a
// background clone's context is the Runner's rather than a request's that
// is already gone.
package project

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"
)

// defaultConcurrency bounds how many clones run at once. A clone is network
// and disk bound and there is no gain to running the whole fleet's worth at
// once; a small pool keeps a burst of Syncs from exhausting either.
const defaultConcurrency = 4

// syncStore is the slice of Store a Runner drives: it claims a project,
// records the outcome, and clears claims a restart stranded. A narrow view
// so a test drives it with three methods rather than the whole port.
type syncStore interface {
	BeginSync(ctx context.Context, id int) (Project, error)
	RecordSync(ctx context.Context, id int, result Result) error
	ResetInterruptedSyncs(ctx context.Context) (int, error)
}

// Runner clones projects asynchronously.
type Runner struct {
	store  syncStore
	syncer Syncer
	logger *slog.Logger

	// ctx is the Runner's own context, cancelled on Shutdown so an
	// in-flight clone stops rather than running past the process it belongs
	// to. It is deliberately NOT the request's context: a request returns
	// the instant a sync is enqueued, and a clone bound to it would be
	// cancelled the moment it began.
	ctx    context.Context
	cancel context.CancelFunc

	sem chan struct{}
	wg  sync.WaitGroup
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithConcurrency bounds how many clones run at once. A value below one is
// ignored, since a runner that can run nothing is never what a caller means.
func WithConcurrency(n int) RunnerOption {
	return func(r *Runner) {
		if n >= 1 {
			r.sem = make(chan struct{}, n)
		}
	}
}

// NewRunner builds a Runner over a store and a syncer. A nil logger is
// replaced with one that discards, so no call site has to guard it.
func NewRunner(store syncStore, syncer Syncer, logger *slog.Logger, opts ...RunnerOption) *Runner {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{
		store:  store,
		syncer: syncer,
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
		sem:    make(chan struct{}, defaultConcurrency),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Enqueue claims a project and starts its sync in the background.
//
// It returns a synchronous error only for a refusal the caller should see
// on the control they pressed: the project is gone, has no fetchable
// source, or is already syncing. Once a clone is under way it returns nil,
// and the clone's own outcome is recorded rather than returned, because by
// then there is no caller left holding the request.
func (r *Runner) Enqueue(ctx context.Context, id int) error {
	claimed, err := r.store.BeginSync(ctx, id)
	if err != nil {
		return err
	}
	r.wg.Add(1)
	go r.run(claimed)
	return nil
}

// run performs one claimed project's clone under the Runner's context and
// records the result.
func (r *Runner) run(p Project) {
	defer r.wg.Done()

	select {
	case r.sem <- struct{}{}:
	case <-r.ctx.Done():
		// A shutdown reached us before the pool had room. Record the claim
		// as failed rather than leaving the row running, which a restart
		// would otherwise have to clear.
		r.record(p.ID, Result{Status: SyncFailed, Err: "the server is shutting down; sync again", At: time.Now()})
		return
	}
	defer func() { <-r.sem }()

	result, err := r.syncer.Sync(r.ctx, p)
	if err != nil {
		// The syncer reserves its error return for a failure to even
		// attempt. BeginSync already refused an unsyncable project, so this
		// is rare; recording it as a failed sync keeps the row honest
		// rather than leaving it running.
		result = Result{Status: SyncFailed, Err: err.Error(), At: time.Now()}
	}
	r.record(p.ID, result)
}

// record writes a sync outcome under a background context, because the
// Runner's own may already be cancelled by a shutdown and the row must
// still be moved off running.
func (r *Runner) record(id int, result Result) {
	if err := r.store.RecordSync(context.Background(), id, result); err != nil {
		r.logger.Error("recording a sync outcome failed",
			slog.Int("project", id), slog.String("error", err.Error()))
	}
}

// RecoverInterrupted clears syncs a previous process left claimed, and is
// meant to run once at startup. Without it a project a crash caught
// mid-clone would stay running forever, and BeginSync would refuse every
// future Sync of it.
func (r *Runner) RecoverInterrupted(ctx context.Context) {
	n, err := r.store.ResetInterruptedSyncs(ctx)
	if err != nil {
		r.logger.Error("resetting interrupted syncs at startup failed",
			slog.String("error", err.Error()))
		return
	}
	if n > 0 {
		r.logger.Info("cleared syncs a restart interrupted", slog.Int("count", n))
	}
}

// Shutdown cancels in-flight clones and waits for them to unwind, up to
// ctx's deadline. A clone that does not stop in time is abandoned rather
// than held onto; its row is left for the next startup's RecoverInterrupted.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Wait blocks until every enqueued sync has finished. It exists so a test
// can make an enqueue deterministic without reaching into the pool.
func (r *Runner) Wait() { r.wg.Wait() }
