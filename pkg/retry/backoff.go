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
func Backoff(base, max time.Duration, attempt int) time.Duration {
	// Exponential backoff: base * 2^attempt.
	sleep := float64(base) * float64(int64(1)<<attempt)

	// Add 0-10% jitter on top of the doubled delay, so retries spread
	// out instead of firing in lockstep. math/rand, not crypto/rand: this
	// timing jitter is not security-sensitive (nothing secret depends on
	// it being unpredictable), and crypto/rand is unnecessary overhead in
	// a hot retry path.
	jitter := sleep * 0.1 * rand.Float64() // #nosec G404 -- intentional, see comment above
	sleep += jitter

	d := time.Duration(sleep)
	if d > max {
		return max
	}
	return d
}
