// Package retry provides small, dependency-free retry-timing helpers
// shared across the platform's polling and reconnect loops.
package retry

import (
	"math/rand"
	"time"
)

// Backoff computes how long to sleep before the next retry attempt,
// given a base delay, a hard ceiling max, and the number of attempts
// already made (attempt, zero-based: attempt 0 computes the delay
// before the very first retry).
//
// The delay doubles with each attempt (base * 2^attempt, the classic
// exponential backoff curve), then has 0-10% random jitter added on top
// before being capped at max.
//
// Jitter exists for the same reason .SPECIFICATION/PATTERNS.md's
// "Exponential Backoff and Jitter (Retry Pattern)" entry documents it
// for this platform: if a network segment blips, thousands of Runners
// retrying on the exact same clock would hammer the target devices in a
// tight, synchronized loop the instant the network recovers, which
// looks like a self-inflicted DDoS rather than a graceful recovery.
// Randomizing each retry's delay slightly spreads that reconnect storm
// out instead of leaving every caller retry at the exact same instant.
// maxAttemptShift bounds the exponent actually used in base*2^attempt.
// Shifting an int64 by 63 or more bits overflows into a negative value
// (attempt==63, the sign bit) or zero (attempt>=64, Go's shift semantics
// for a count that reaches the operand's own bit width), and both feed a
// non-positive sleep into the max clamp below, which only fires on
// d > max: a non-positive d is never greater than a positive max, so the
// clamp is silently bypassed instead of returning max, producing an
// effectively-immediate retry instead of the intended ceiling. 1<<62 is
// already far larger than any realistic base/max ratio this platform
// configures, so capping the shift here, before it ever executes, keeps
// every later step (the shift itself, the float64 multiply, the
// float64->time.Duration conversion) inside a range this formula was
// actually designed for, rather than relying on the final comparison to
// catch an already-corrupted value.
const maxAttemptShift = 62

func Backoff(base, max time.Duration, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	shift := attempt
	if shift > maxAttemptShift {
		shift = maxAttemptShift
	}

	// Exponential backoff: base * 2^attempt, attempt capped per
	// maxAttemptShift's own doc comment.
	sleep := float64(base) * float64(int64(1)<<uint(shift)) // #nosec G115 -- shift is bounded to [0, maxAttemptShift] immediately above

	// Add 0-10% jitter on top of the doubled delay, so retries spread
	// out instead of firing in lockstep. math/rand, not crypto/rand: this
	// timing jitter is not security-sensitive (nothing secret depends on
	// it being unpredictable), and crypto/rand is unnecessary overhead in
	// a hot retry path.
	jitter := sleep * 0.1 * rand.Float64() // #nosec G404 -- intentional, see comment above
	sleep += jitter

	d := time.Duration(sleep)
	// d <= 0 is the belt-and-suspenders half of maxAttemptShift's own
	// defense: even with the shift capped, an unusually large base could
	// still overflow the float64->time.Duration conversion. A non-positive
	// result is never legitimate output of this formula, so it is treated
	// as "cap reached" exactly like d > max already is.
	if d <= 0 || d > max {
		return max
	}
	return d
}
