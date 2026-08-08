package retry_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
)

// TestBackoff_DoublesPerAttempt proves the exponential curve: for a
// given attempt, the pre-jitter delay is base * 2^attempt, and jitter
// only ever adds 0-10% on top of that, never subtracts. Since jitter
// makes any single call non-deterministic, each attempt is sampled many
// times and every sample must fall in the documented
// [wantBase, wantBase*1.10] band.
func TestBackoff_DoublesPerAttempt(t *testing.T) {
	base := 100 * time.Millisecond
	maxDelay := time.Hour // effectively uncapped, so the cap does not interfere with this test

	tests := []struct {
		attempt  int
		wantBase time.Duration // pre-jitter delay: base * 2^attempt
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{5, 3200 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.wantBase.String(), func(t *testing.T) {
			upperBound := time.Duration(float64(tt.wantBase) * 1.10)
			for i := 0; i < 200; i++ {
				got := retry.Backoff(base, maxDelay, tt.attempt)
				if got < tt.wantBase || got > upperBound {
					t.Fatalf("Backoff(%v, %v, %d) = %v, want in [%v, %v]", base, maxDelay, tt.attempt, got, tt.wantBase, upperBound)
				}
			}
		})
	}
}

// TestBackoff_CapsAtMax proves the max ceiling is enforced even when the
// doubled, jittered delay would otherwise exceed it by a wide margin.
func TestBackoff_CapsAtMax(t *testing.T) {
	base := 100 * time.Millisecond
	maxDelay := 500 * time.Millisecond

	// attempt 10 means a pre-jitter delay of 100ms * 2^10 = 102.4s, far
	// past maxDelay; every sample must still land exactly at the cap.
	for i := 0; i < 200; i++ {
		got := retry.Backoff(base, maxDelay, 10)
		if got != maxDelay {
			t.Fatalf("Backoff() = %v, want exactly the cap %v", got, maxDelay)
		}
	}
}

// TestBackoff_JitterStaysWithinDocumentedBand is a statistical check
// (many samples, not a single flaky exact-equality assertion) that
// jitter never pushes the result below base and never above base*1.10,
// and that jitter is not a no-op dressed up as randomness: across many
// samples at least one must differ from the unjittered base.
func TestBackoff_JitterStaysWithinDocumentedBand(t *testing.T) {
	base := 1 * time.Second
	maxDelay := time.Hour
	const attempt = 0 // pre-jitter delay is exactly base at attempt 0

	upperBound := time.Duration(float64(base) * 1.10)
	const samples = 1000
	sawJitter := false

	for i := 0; i < samples; i++ {
		got := retry.Backoff(base, maxDelay, attempt)
		if got < base {
			t.Fatalf("Backoff() = %v, must never be below base %v (jitter only adds, never subtracts)", got, base)
		}
		if got > upperBound {
			t.Fatalf("Backoff() = %v, exceeds the documented 10%% jitter band (max %v)", got, upperBound)
		}
		if got > base {
			sawJitter = true
		}
	}
	if !sawJitter {
		t.Fatalf("expected at least one of %d samples to show nonzero jitter", samples)
	}
}

// TestBackoff_ZeroAttemptNeverBelowBase proves attempt 0's result is
// never less than base, the simplest sanity check on the formula's
// starting point.
func TestBackoff_ZeroAttemptNeverBelowBase(t *testing.T) {
	base := 50 * time.Millisecond
	maxDelay := time.Hour

	got := retry.Backoff(base, maxDelay, 0)
	if got < base {
		t.Fatalf("Backoff() = %v, want >= base %v", got, base)
	}
}

// TestBackoff_HighAttemptClampsToMax is the regression test for the shift
// overflow bug: base*2^attempt shifts an int64 by attempt bits before any
// clamping exists, so attempt==63 overflows into a negative value (the
// sign bit) and attempt>=64 shifts all the way to zero. Both used to
// produce a non-positive Duration, which is never greater than max, so
// the max clamp was silently bypassed instead of firing -- an
// effectively-immediate retry at exactly the attempt counts a real,
// long-idle Runner (Agent.Run's retries counter has no ceiling) would
// eventually reach. Every attempt in this table must still clamp to
// exactly max, many samples each since jitter makes a single sample
// insufficient to catch a rare negative excursion.
func TestBackoff_HighAttemptClampsToMax(t *testing.T) {
	base := 100 * time.Millisecond
	maxDelay := 10 * time.Second

	for _, attempt := range []int{62, 63, 64, 65, 100, 1000, math.MaxInt32} {
		t.Run(fmt.Sprintf("attempt=%d", attempt), func(t *testing.T) {
			for i := 0; i < 200; i++ {
				got := retry.Backoff(base, maxDelay, attempt)
				if got != maxDelay {
					t.Fatalf("Backoff(%v, %v, %d) = %v, want exactly the cap %v", base, maxDelay, attempt, got, maxDelay)
				}
			}
		})
	}
}

// TestBackoff_NeverReturnsNonPositive sweeps attempt across every
// boundary value where the shift-before-clamp bug could resurface
// (including negative attempts, which the fixed function now clamps to 0
// rather than shifting by a negative count, undefined in Go), asserting
// the one invariant this function must never violate: a positive base and
// a positive max must always produce a positive result no larger than max.
func TestBackoff_NeverReturnsNonPositive(t *testing.T) {
	base := 10 * time.Millisecond
	maxDelay := 5 * time.Second

	for _, attempt := range []int{-100, -1, 0, 1, 2, 30, 61, 62, 63, 64, 65, 200, math.MaxInt32, math.MinInt32} {
		got := retry.Backoff(base, maxDelay, attempt)
		if got <= 0 || got > maxDelay {
			t.Fatalf("Backoff(%v, %v, %d) = %v, want in (0, %v]", base, maxDelay, attempt, got, maxDelay)
		}
	}
}

// FuzzBackoff proves Backoff's one load-bearing invariant, 0 < got <=
// max, holds across arbitrary attempt values, including the ones a
// human-written table might not think to include. base and max are also
// fuzzed, restricted to a strictly-positive range: base<=0 or max<=0
// describe a caller misconfiguration this function has never claimed to
// handle, not a case its own invariant covers.
func FuzzBackoff(f *testing.F) {
	f.Add(100*int64(time.Millisecond), 10*int64(time.Second), 0)
	f.Add(100*int64(time.Millisecond), 10*int64(time.Second), 62)
	f.Add(100*int64(time.Millisecond), 10*int64(time.Second), 63)
	f.Add(100*int64(time.Millisecond), 10*int64(time.Second), 64)
	f.Add(100*int64(time.Millisecond), 10*int64(time.Second), 1000)
	f.Add(int64(1), int64(1), -1)
	f.Add(int64(1), int64(1), math.MaxInt32)

	f.Fuzz(func(t *testing.T, baseNanos, maxNanos int64, attempt int) {
		if baseNanos <= 0 || maxNanos <= 0 {
			return
		}
		base := time.Duration(baseNanos)
		maxDelay := time.Duration(maxNanos)

		got := retry.Backoff(base, maxDelay, attempt)
		if got <= 0 {
			t.Fatalf("Backoff(%v, %v, %d) = %v, must be > 0", base, maxDelay, attempt, got)
		}
		if got > maxDelay {
			t.Fatalf("Backoff(%v, %v, %d) = %v, must be <= max %v", base, maxDelay, attempt, got, maxDelay)
		}
	})
}
