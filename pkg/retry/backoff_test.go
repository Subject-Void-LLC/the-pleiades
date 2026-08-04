package retry_test

import (
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/retry"
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
