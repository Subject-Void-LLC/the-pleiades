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
// stranded is cleared by the next recovery sweep (RecoverInterrupted). What the
// Runner adds is the concurrency, the lifecycle, and the promise that a
// background clone's context is the Runner's rather than a request's that
// is already gone.
package project

import (
	"context"
	"io"
	"log/slog"
	"strings"
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
	BeginSync(ctx context.Context, id int, actor string) (Claim, error)
	RecordSync(ctx context.Context, id int, result Result) error
	ResetInterruptedSyncs(ctx context.Context, alive []string) (int, error)

	// ByLaunchable resolves the launchable reference a schedule fires with
	// into the project it stands for. It is here rather than on a separate
	// port because the Runner is what a schedule launches through
	// (runner_launch.go), so resolving that reference is part of what this
	// Runner needs from a store rather than a second collaborator.
	ByLaunchable(ctx context.Context, launchableID int) (Project, error)
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

	// mu guards running, which holds one cancel per clone in flight so a
	// single sync can be stopped without disturbing the others.
	mu      sync.Mutex
	running map[int]context.CancelFunc

	// progress is where a running clone's output goes so somebody can watch
	// it. The Runner owns it because the Runner is what starts and ends a
	// clone, which is exactly when a stream opens and closes.
	progress *Progress
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
		store:    store,
		syncer:   syncer,
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
		sem:      make(chan struct{}, defaultConcurrency),
		progress: NewProgress(),
		running:  map[int]context.CancelFunc{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Enqueue claims a project and starts its sync in the background, and
// returns the id of the attempt it started.
//
// The id is what lets whoever asked for the sync say which attempt they
// started: a schedule records it on the occurrence it fired. A caller with
// no use for it ignores it, which is every caller that has a page to
// redirect to instead.
//
// It returns a synchronous error only for a refusal the caller should see
// on the control they pressed: the project is gone, has no fetchable
// source, is already syncing, or named no actor. Once a clone is under way
// the error is nil, and the clone's own outcome is recorded rather than
// returned, because by then there is no caller left holding the request.
func (r *Runner) Enqueue(ctx context.Context, id int, actor string) (int, error) {
	// Refused here as well as in the store. The store is the real boundary
	// and enforces it for every caller, including one that never comes
	// through a Runner; this check is so a caller that forgot finds out
	// before a background goroutine exists to tell.
	if strings.TrimSpace(actor) == "" {
		return 0, ErrNoActor
	}

	claim, err := r.store.BeginSync(ctx, id, actor)
	if err != nil {
		return 0, err
	}
	r.wg.Add(1)
	go r.run(claim)
	return claim.RunID, nil
}

// run performs one claimed project's clone under the Runner's context and
// records the result against the attempt the claim opened.
func (r *Runner) run(claim Claim) {
	defer r.wg.Done()
	p := claim.Project

	// The claim's own start, not a second reading of the clock here: the
	// history row already holds this instant, and two stamps a few
	// microseconds apart would be two answers to one question.
	started := claim.StartedAt

	select {
	case r.sem <- struct{}{}:
	case <-r.ctx.Done():
		// A shutdown reached us before the pool had room. Record the claim
		// as failed rather than leaving the row running, which a restart
		// would otherwise have to clear.
		r.record(p.ID, Result{
			Status:    SyncFailed,
			Err:       "the server is shutting down; sync again",
			At:        time.Now(),
			StartedAt: started,
			RunID:     claim.RunID,
		})
		return
	}
	defer func() { <-r.sem }()

	// A context of this clone's own, derived from the Runner's, so Cancel
	// can stop one sync without touching any other. Shutdown still reaches
	// all of them, because they all descend from r.ctx.
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	r.track(p.ID, cancel)
	defer r.untrack(p.ID)

	// The clone reports into this project's stream, and the stream ends
	// when the clone does however it ends, so a reader is never left
	// watching a page that will receive nothing more.
	out := r.progress.Writer(p.ID)
	defer r.progress.Finish(p.ID)

	result, err := r.syncer.Sync(ctx, p, out)
	if err != nil {
		// The syncer reserves its error return for a failure to even
		// attempt. BeginSync already refused an unsyncable project, so this
		// is rare; recording it as a failed sync keeps the row honest
		// rather than leaving it running.
		result = Result{Status: SyncFailed, Err: err.Error(), At: time.Now()}
	}

	// A cancelled clone is recorded as cancelled rather than as whatever
	// the transport said when its context went away, which is usually a
	// message about a closed connection that reads like a network fault.
	// The distinction that matters is WHOSE doing it was: a shutdown is the
	// server's, and a cancel is a person's.
	if ctx.Err() != nil {
		result.Status = SyncFailed
		result.Revision = ""
		if r.ctx.Err() != nil {
			result.Err = "the server was shutting down, so the sync was stopped; sync again"
		} else {
			result.Err = "the sync was cancelled"
		}
		if result.At.IsZero() {
			result.At = time.Now()
		}
	}

	result.StartedAt = started
	result.RunID = claim.RunID
	r.record(p.ID, result)
}

// track remembers how to stop one clone while it runs.
func (r *Runner) track(projectID int, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running[projectID] = cancel
}

// untrack forgets a clone that has finished, so a later Cancel of the same
// project reports honestly that nothing was running rather than cancelling a
// context nobody is waiting on.
func (r *Runner) untrack(projectID int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, projectID)
}

// Cancel stops this project's running clone and reports whether there was
// one. It returns as soon as the clone is told to stop: the outcome is
// recorded by the goroutine that was running it, the same way every other
// ending is.
func (r *Runner) Cancel(projectID int) bool {
	r.mu.Lock()
	cancel, ok := r.running[projectID]
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
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

// RecoverInterrupted clears syncs no live process is still running. A
// controller calls it at startup and on every heartbeat, with the instance
// ids it knows to be alive, its own included; nil treats every claim as
// abandoned, which is right for a lone process at startup. Without it a
// project a crash caught mid-clone would stay running forever, and BeginSync
// would refuse every future Sync of it.
func (r *Runner) RecoverInterrupted(ctx context.Context, alive []string) {
	n, err := r.store.ResetInterruptedSyncs(ctx, alive)
	if err != nil {
		r.logger.Error("resetting interrupted syncs failed",
			slog.String("error", err.Error()))
		return
	}
	if n > 0 {
		r.logger.Info("cleared syncs no running controller owns", slog.Int("count", n))
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

// Subscribe returns this project's live clone output: the lines already
// written, then new ones, with the channel closed when the clone finishes.
// The returned func releases the subscription and must be called.
//
// It is a passthrough to the Runner's own Progress rather than an exposed
// field, so a caller serving a reader depends on the Runner it already has
// instead of on a second value wired beside it.
func (r *Runner) Subscribe(projectID int) (<-chan string, func()) {
	return r.progress.Subscribe(projectID)
}
