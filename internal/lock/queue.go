package lock

import (
	"context"
	"errors"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
)

// queueBaseBackoff and queueMaxBackoff bound acquireWithContention's retry
// delay under PolicyQueue and PolicyPriority. A short base keeps a lightly
// contended lock responsive; the cap keeps a heavily contended one from
// making a waiting caller sleep for minutes at a stretch between attempts.
const (
	queueBaseBackoff     = 50 * time.Millisecond
	queueMaxBackoff      = 5 * time.Second
	minPriorityPollDelay = 5 * time.Millisecond
)

// acquireWithContention implements PolicyQueue and PolicyPriority once,
// shared by every Manager adapter, so the retry-with-backoff loop exists in
// exactly one place (PLAN.md Section 25's "Retry policy... lock
// acquisition" row names this exact duplication risk, and Phase 72 closed
// the remaining half of it: the loop itself, not just contentionBackoff's
// delay math, now comes from the single shared pkg/retry.Do rather than
// being hand-rolled here). tryOnce must attempt a single Acquire and return
// ErrLockHeld (or a wrapped ErrLockHeld) on contention; any other error, or
// a nil error, is returned immediately without retrying.
//
// Under PolicyReject, retryable below always reports false, so retry.Do
// calls tryOnce exactly once and returns its result unchanged.
//
// Under PolicyQueue and PolicyPriority both, this retries tryOnce until it
// stops returning ErrLockHeld or ctx is done, at which point retry.Do
// returns an error wrapping ctx.Err() (still satisfying
// errors.Is(err, context.Canceled) or errors.Is(err, context.DeadlineExceeded)
// for a caller checking either). The two policies share this identical
// retry shape and differ only in backoff cadence: PolicyQueue uses a
// uniform backoff regardless of AcquireOptions.Priority; PolicyPriority
// scales the same backoff down by Priority, so among several simultaneous
// callers waiting on the same itemID, a higher-Priority one polls more
// often and is statistically more likely to win the race the instant the
// itemID frees up. This is a deliberately soft, probabilistic preference
// among waiting contenders, not a hard guarantee and never a preemption of
// a lock that is still within its declared window: no adapter's
// tryAcquireOnce ever inspects or acts on AcquireOptions.Priority against a
// live holder. An earlier draft of this design had a priority caller
// eagerly steal an already-expired entry ahead of the store's own lazy
// cleanup; that degenerated to a no-op once per-key TTL made passive
// reclaim already available to every policy identically (see
// LESSONS_LEARNED.md), so priority is expressed here, entirely
// client-side, instead.
func acquireWithContention(ctx context.Context, opts AcquireOptions, tryOnce func(context.Context) (Lease, error)) (Lease, error) {
	retryable := func(err error) bool {
		return (opts.Policy == PolicyQueue || opts.Policy == PolicyPriority) && errors.Is(err, ErrLockHeld)
	}
	delay := func(attempt int) time.Duration { return contentionBackoff(opts, attempt) }

	// maxAttempts 0: unlimited, bounded only by ctx or retryable turning
	// false, exactly PolicyQueue/PolicyPriority's original "wait until it
	// stops being held or ctx gives up" contract.
	return retry.Do(ctx, delay, 0, retryable, tryOnce)
}

// contentionBackoff computes how long acquireWithContention should wait
// before its next retry. PolicyPriority divides the base backoff by
// (Priority+1), floored at minPriorityPollDelay so a very high priority
// value cannot busy-loop; a non-positive Priority behaves exactly like
// PolicyQueue's uniform backoff.
func contentionBackoff(opts AcquireOptions, attempt int) time.Duration {
	d := retry.Backoff(queueBaseBackoff, queueMaxBackoff, attempt)
	if opts.Policy != PolicyPriority || opts.Priority <= 0 {
		return d
	}
	d /= time.Duration(opts.Priority + 1)
	if d < minPriorityPollDelay {
		d = minPriorityPollDelay
	}
	return d
}
