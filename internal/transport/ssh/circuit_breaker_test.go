package ssh

import (
	"sync"
	"testing"
	"time"
)

// TestCircuitBreaker_OpensAfterThreshold proves a target's circuit
// transitions to open exactly once consecutiveFailures reaches
// threshold, not before.
func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	b := newCircuitBreaker(3, time.Minute)
	const key = "host:22"

	for i := 0; i < 2; i++ {
		if !b.Allow(key) {
			t.Fatalf("attempt %d: expected Allow to be true before threshold is reached", i)
		}
		b.RecordFailure(key)
	}

	// Two failures recorded, threshold is 3: still closed.
	if !b.Allow(key) {
		t.Fatal("expected circuit still closed after 2 of 3 threshold failures")
	}
	b.RecordFailure(key)

	// Third consecutive failure reaches threshold: now open.
	if b.Allow(key) {
		t.Fatal("expected circuit open after reaching threshold consecutive failures")
	}
}

// TestCircuitBreaker_FailsFastWhileOpen proves that once open, Allow
// returns false repeatedly with no state change, until cooldown elapses.
func TestCircuitBreaker_FailsFastWhileOpen(t *testing.T) {
	b := newCircuitBreaker(1, time.Hour) // cooldown far longer than this test can run
	const key = "host:22"

	b.RecordFailure(key) // one failure reaches threshold of 1, opens immediately

	for i := 0; i < 5; i++ {
		if b.Allow(key) {
			t.Fatalf("call %d: expected Allow to fail fast while circuit is open", i)
		}
	}
}

// TestCircuitBreaker_HalfOpenAfterCooldown proves that once cooldown has
// elapsed, exactly one Allow call is let through as a probe, and further
// concurrent/subsequent calls are denied until that probe's outcome is
// recorded.
func TestCircuitBreaker_HalfOpenAfterCooldown(t *testing.T) {
	const cooldown = 20 * time.Millisecond
	b := newCircuitBreaker(1, cooldown)
	const key = "host:22"

	b.RecordFailure(key) // opens immediately, threshold 1

	if b.Allow(key) {
		t.Fatal("expected circuit still open before cooldown elapses")
	}

	time.Sleep(cooldown + 10*time.Millisecond)

	if !b.Allow(key) {
		t.Fatal("expected exactly one probe dial to be allowed once cooldown elapsed")
	}

	// The probe's outcome has not been recorded yet; the circuit is
	// half-open, so a second caller must be denied.
	if b.Allow(key) {
		t.Fatal("expected a second concurrent caller to be denied while a probe is in flight")
	}
}

// TestCircuitBreaker_ClosesOnSuccessfulProbe proves a successful
// half-open probe fully closes the circuit and resets the failure
// streak, so a fresh run of failures is needed to reopen it.
func TestCircuitBreaker_ClosesOnSuccessfulProbe(t *testing.T) {
	const cooldown = 10 * time.Millisecond
	b := newCircuitBreaker(2, cooldown)
	const key = "host:22"

	b.RecordFailure(key)
	b.RecordFailure(key) // reaches threshold of 2, opens

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected the probe dial to be allowed after cooldown")
	}

	b.RecordSuccess(key)

	if !b.Allow(key) {
		t.Fatal("expected circuit closed immediately after a successful probe")
	}

	// Confirm the failure streak actually reset: one failure alone must
	// not reopen a threshold-2 circuit.
	b.RecordFailure(key)
	if !b.Allow(key) {
		t.Fatal("expected circuit still closed after only 1 of 2 threshold failures post-reset")
	}
}

// TestCircuitBreaker_ReopensOnFailedProbe proves a failed half-open
// probe reopens the circuit and restarts its cooldown clock, regardless
// of the configured threshold (a single failed probe is always enough).
func TestCircuitBreaker_ReopensOnFailedProbe(t *testing.T) {
	const cooldown = 10 * time.Millisecond
	b := newCircuitBreaker(5, cooldown) // high threshold: only the probe-failure path can reopen this fast
	const key = "host:22"

	for i := 0; i < 5; i++ {
		b.RecordFailure(key)
	}
	if b.Allow(key) {
		t.Fatal("expected circuit open after reaching threshold")
	}

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected the probe dial to be allowed after cooldown")
	}

	// The probe itself fails.
	b.RecordFailure(key)

	// Immediately after a failed probe, the circuit must be open again
	// (cooldown clock restarted), not accidentally left half-open or
	// closed.
	if b.Allow(key) {
		t.Fatal("expected circuit reopened immediately after a failed probe")
	}

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected a fresh probe to be allowed once the restarted cooldown elapses")
	}
}

// TestCircuitBreaker_ConcurrentUse hammers Allow/RecordSuccess/
// RecordFailure across many goroutines and many keys, run under -race,
// to prove the shared entries map is safe for concurrent use. It makes
// no behavioral assertion beyond "does not race and does not panic";
// the single-threaded tests above already cover the state machine's
// correctness.
func TestCircuitBreaker_ConcurrentUse(t *testing.T) {
	b := newCircuitBreaker(3, 5*time.Millisecond)
	keys := []string{"a:22", "b:22", "c:22"}

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 200
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			key := keys[g%len(keys)]
			for i := 0; i < iterations; i++ {
				if b.Allow(key) {
					if i%2 == 0 {
						b.RecordSuccess(key)
					} else {
						b.RecordFailure(key)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
