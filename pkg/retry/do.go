package retry

import (
	"context"
	"fmt"
	"time"
)

// Do calls fn, and keeps calling it, one attempt at a time, until one of
// three things happens: fn returns a nil error (success), fn returns an
// error retryable reports false for (a non-retryable failure), or ctx is
// done. Between a failed, retryable attempt and the next, Do sleeps
// delay(attempt), where attempt is the zero-based index of the attempt
// that just failed, returning ctx's own error (wrapped together with the
// attempt's own error) if ctx finishes first.
//
// maxAttempts caps the total number of times fn is called. maxAttempts <= 0
// means unlimited: Do keeps retrying until ctx is done or retryable
// returns false, exactly the shape internal/lock's own queued lock
// acquisition needs (PLAN.md Section 25's Build-Once table names this
// exact duplication risk: a retry loop hand-rolled once per caller). A
// positive maxAttempts is what pkg/remoteexec's dial phase needs instead,
// since dialing a target forever is never the right default.
//
// Do owns the loop and the sleep. It does not own deciding what counts as
// retryable (that is retryable's job, and a caller like a circuit breaker
// check can report its own "do not retry, fail fast" verdict through it)
// and it does not own logging or metrics, which stay at the call site.
//
// ctx ending is always terminal, regardless of what retryable says about
// the error fn returned: retrying an operation past a deadline or
// cancellation that already stopped it is never correct, and treating it
// as just another retryable error would race the sleep below against an
// already-closed ctx.Done() nondeterministically (sometimes one more
// attempt slips through, sometimes it does not). Checking ctx.Err()
// immediately after every attempt, before consulting retryable at all,
// keeps that deterministic no matter how fn itself reacts to ctx.
func Do[T any](ctx context.Context, delay func(attempt int) time.Duration, maxAttempts int, retryable func(error) bool, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	var lastErr error

	for attempt := 0; maxAttempts <= 0 || attempt < maxAttempts; attempt++ {
		val, err := fn(ctx)
		if err == nil {
			return val, nil
		}
		lastErr = err

		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, fmt.Errorf("%w: %w", ctxErr, lastErr)
		}

		if !retryable(err) {
			return zero, err
		}

		// Do not sleep after the final permitted attempt; there is
		// nothing left to wait for.
		if maxAttempts > 0 && attempt == maxAttempts-1 {
			break
		}

		if sleepErr := sleepFor(ctx, delay(attempt)); sleepErr != nil {
			return zero, fmt.Errorf("%w: %w", sleepErr, lastErr)
		}
	}

	return zero, fmt.Errorf("retry: exhausted %d attempt(s): %w", maxAttempts, lastErr)
}

// Sleep waits out a jittered exponential backoff delay
// (Backoff(base, max, attempt)), or returns ctx's own error if ctx finishes
// first. It is the same context-aware wait Do uses internally between
// attempts, exported on its own for a caller whose retry unit is not a
// single clean operation Do's fn shape can express.
//
// internal/lock's own contention-handling CAS loops (join/leave a shared
// lock, publish a new value with an optimistic-concurrency revision) are
// exactly that case: several different outcomes and several different side
// effects live inside one loop iteration, so wrapping the whole iteration
// in a single fn callback would not be a simplification. Those loops still
// need the identical "sleep or bail on ctx" primitive between iterations,
// which is what this gives them, without a second hand-rolled copy of
// Do's own timer/select logic.
func Sleep(ctx context.Context, base, max time.Duration, attempt int) error {
	return sleepFor(ctx, Backoff(base, max, attempt))
}

// sleepFor waits out d, or returns ctx's own error if ctx finishes first.
func sleepFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
