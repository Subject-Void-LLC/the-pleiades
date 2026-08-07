// Package runner: Agent's own pull loop and bounded worker pool, split
// into its own sibling file, following internal/dispatch's own
// worker.go/worker_devices.go/worker_config.go split, once agent.go grew
// past AGENTS.md's ~300-line soft cap on logic files.
package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/retry"
	"github.com/nats-io/nats.go/jetstream"
)

// Run starts the pull loop and its bounded worker pool. It blocks until
// ctx is canceled, then drains in-flight work before returning: fetchLoop
// stops issuing new fetches, jobs is closed, and Run waits for every
// worker to finish its own current message before returning (Graceful
// Shutdown, PATTERNS.md's own entry of that name), rather than abandoning
// a message mid-handleMessage the instant ctx is done.
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info("agent starting pull loop", slog.Int("pool_size", a.poolSize))

	jobs := make(chan jetstream.Msg, a.poolSize)
	var wg sync.WaitGroup
	for i := 0; i < a.poolSize; i++ {
		wg.Add(1)
		go a.worker(ctx, jobs, &wg)
	}

	runErr := a.fetchLoop(ctx, jobs)

	close(jobs)
	wg.Wait()
	a.logger.Info("agent worker pool drained")
	return runErr
}

// worker is one bounded worker-pool goroutine (PATTERNS.md's own Worker
// Pool entry, "a fixed number of goroutines... pulling tasks from a
// queue"): it pulls jetstream.Msg values off jobs, calling handleMessage
// for each, until Run closes jobs. Up to a.poolSize workers can be
// handling a message concurrently, which is what makes executeWithLease's
// per-device lock (agent_exec.go) load-bearing rather than decorative:
// without it, two workers could legitimately race the same device.
func (a *Agent) worker(ctx context.Context, jobs <-chan jetstream.Msg, wg *sync.WaitGroup) {
	defer wg.Done()
	for msg := range jobs {
		a.handleMessage(ctx, msg)
	}
}

// fetchLoop is Run's pull side: repeatedly calls FetchNoWait(a.poolSize)
// and feeds every message received into jobs, backing off (via
// backoffSleep) on either a real fetch error or a drained-but-empty
// batch. Fetching a.poolSize messages per call, rather than a hardcoded
// 1, is what actually lets the worker pool run concurrently -- fetching
// one at a time would starve every worker but the first.
//
// A drained-but-empty fetch (FetchNoWait returning no error and zero
// messages, the ordinary "nothing waiting right now" outcome) used to
// fall through to an immediate re-fetch with no backoff at all, a hot
// spin distinct from the error-path backoff that already worked; this is
// what closes that gap, treating received == 0 the same as a real error.
func (a *Agent) fetchLoop(ctx context.Context, jobs chan<- jetstream.Msg) error {
	retries := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			msgs, err := a.consumer.FetchNoWait(a.poolSize)
			if err != nil {
				a.logger.Debug("fetch error, backing off", slog.String("error", err.Error()))
				if waitErr := a.backoffSleep(ctx, retries); waitErr != nil {
					return waitErr
				}
				retries++
				continue
			}

			received, sendErr := a.dispatchMessages(ctx, msgs, jobs)
			if sendErr != nil {
				return sendErr
			}

			if received == 0 {
				if waitErr := a.backoffSleep(ctx, retries); waitErr != nil {
					return waitErr
				}
				retries++
				// An idle tick with spare cycles is also this Agent's
				// natural point to retry delivering any WAL entry a
				// prior flush attempt could not reach (agent_wal.go); a
				// nil a.wal makes this a no-op for every Agent that
				// never opted into WithResultWAL.
				a.flushWAL(ctx)
				continue
			}
			retries = 0
		}
	}
}

// dispatchMessages drains every message batch's own Messages() channel
// into jobs, returning how many were received. It returns ctx's own
// error, without draining further, the moment jobs <- msg cannot proceed
// because ctx is done: a message already pulled off JetStream in that
// case is deliberately neither Ack'd nor Nak'd here, since JetStream's own
// AckWait redelivers it once this delivery's ack window lapses, the same
// at-least-once guarantee every other in-flight, not-yet-acked message
// already relies on.
func (a *Agent) dispatchMessages(ctx context.Context, msgs jetstream.MessageBatch, jobs chan<- jetstream.Msg) (int, error) {
	received := 0
	for msg := range msgs.Messages() {
		received++
		select {
		case jobs <- msg:
		case <-ctx.Done():
			return received, ctx.Err()
		}
	}
	if msgs.Error() != nil {
		a.logger.Error("message stream error", slog.String("error", msgs.Error().Error()))
	}
	return received, nil
}

// backoffSleep sleeps for calculateBackoff(retries) or returns ctx's own
// error if ctx is done first. fetchLoop's two backoff sites (a real fetch
// error, and a drained-but-empty fetch) both call this so they share one
// sleep implementation rather than two hand-copied selects that could
// silently diverge.
func (a *Agent) backoffSleep(ctx context.Context, retries int) error {
	sleepDuration := a.calculateBackoff(retries)
	a.logger.Debug("backing off before next fetch", slog.Duration("sleep", sleepDuration))
	select {
	case <-time.After(sleepDuration):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// calculateBackoff computes how long to sleep before the next poll
// retry, using the shared jittered exponential backoff formula in
// pkg/retry (see retry.Backoff's doc comment for the algorithm and why
// jitter matters). This method is kept, rather than calling
// retry.Backoff directly at its call sites, so a.baseSleep and a.maxSleep
// stay the only things a caller needs to know about; it is a pure
// delegation with no behavior of its own.
func (a *Agent) calculateBackoff(retries int) time.Duration {
	return retry.Backoff(a.baseSleep, a.maxSleep, retries)
}
