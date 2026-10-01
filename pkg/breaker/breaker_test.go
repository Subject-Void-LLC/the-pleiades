// Tests for the circuit breaker's state machine, its probe lease and its
// safety under concurrent use.
package breaker_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
)

// key is the target every single-target test below uses.
const key = "host:22"

// TestBreaker_OpensAfterThreshold proves a circuit opens exactly when
// the failure streak reaches the threshold, not before.
func TestBreaker_OpensAfterThreshold(t *testing.T) {
	b := breaker.New(3, time.Minute)

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

// TestBreaker_ThresholdBelowOneOpensOnTheFirstFailure pins New's
// documented reading of a threshold below one.
func TestBreaker_ThresholdBelowOneOpensOnTheFirstFailure(t *testing.T) {
	b := breaker.New(0, time.Hour)
	if !b.Allow(key) {
		t.Fatal("expected a target with no history to be allowed")
	}
	b.RecordFailure(key)
	if b.Allow(key) {
		t.Fatal("expected a threshold of zero to open on the first failure")
	}
}

// TestBreaker_FailsFastWhileOpen proves an open circuit keeps refusing,
// with no state change, until the cooldown passes.
func TestBreaker_FailsFastWhileOpen(t *testing.T) {
	b := breaker.New(1, time.Hour) // far longer than this test can run

	b.RecordFailure(key) // threshold of one, so this opens it

	for i := 0; i < 5; i++ {
		if b.Allow(key) {
			t.Fatalf("call %d: expected Allow to fail fast while circuit is open", i)
		}
		if b.Permitted(key) {
			t.Fatalf("look %d: expected Permitted to report the open circuit", i)
		}
	}
}

// TestBreaker_HalfOpenAfterCooldown proves one probe is let through once
// the cooldown passes, and a second caller is refused while it is out.
func TestBreaker_HalfOpenAfterCooldown(t *testing.T) {
	const cooldown = 20 * time.Millisecond
	b := breaker.New(1, cooldown)

	b.RecordFailure(key)
	if b.Allow(key) {
		t.Fatal("expected circuit still open before cooldown elapses")
	}

	time.Sleep(cooldown + 10*time.Millisecond)

	if !b.Allow(key) {
		t.Fatal("expected exactly one probe to be allowed once cooldown elapsed")
	}
	if b.Allow(key) {
		t.Fatal("expected a second caller to be denied while a probe is out")
	}
}

// TestBreaker_ClosesOnSuccessfulProbe proves a successful probe closes
// the circuit and resets the streak, so reopening it takes a fresh run of
// failures.
func TestBreaker_ClosesOnSuccessfulProbe(t *testing.T) {
	const cooldown = 10 * time.Millisecond
	b := breaker.New(2, cooldown)

	b.RecordFailure(key)
	b.RecordFailure(key) // reaches threshold of 2, opens

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected the probe to be allowed after cooldown")
	}

	b.RecordSuccess(key)

	if !b.Allow(key) {
		t.Fatal("expected circuit closed immediately after a successful probe")
	}

	// One failure alone must not reopen a threshold-2 circuit.
	b.RecordFailure(key)
	if !b.Allow(key) {
		t.Fatal("expected circuit still closed after only 1 of 2 threshold failures post-reset")
	}
}

// TestBreaker_ReopensOnFailedProbe proves a failed probe reopens the
// circuit at once, whatever the threshold, and restarts its cooldown.
func TestBreaker_ReopensOnFailedProbe(t *testing.T) {
	const cooldown = 10 * time.Millisecond
	b := breaker.New(5, cooldown) // only the probe-failure path can reopen this fast

	for i := 0; i < 5; i++ {
		b.RecordFailure(key)
	}
	if b.Allow(key) {
		t.Fatal("expected circuit open after reaching threshold")
	}

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected the probe to be allowed after cooldown")
	}

	b.RecordFailure(key) // the probe itself fails

	if b.Allow(key) {
		t.Fatal("expected circuit reopened immediately after a failed probe")
	}

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected a fresh probe to be allowed once the restarted cooldown elapses")
	}
}

// TestBreaker_PermittedDoesNotConsumeTheProbe pins the distinction
// FAILURE_PATTERNS 146 rests on: looking changes nothing, claiming does.
func TestBreaker_PermittedDoesNotConsumeTheProbe(t *testing.T) {
	const cooldown = 10 * time.Millisecond
	b := breaker.New(1, cooldown)
	b.RecordFailure(key) // threshold of one, so the circuit is open

	if b.Permitted(key) {
		t.Error("Permitted reported true inside the cooldown")
	}
	time.Sleep(cooldown + 10*time.Millisecond)

	// Any number of looks must all say yes and leave the probe unclaimed.
	for i := 0; i < 3; i++ {
		if !b.Permitted(key) {
			t.Fatalf("look %d: Permitted reported false after the cooldown elapsed, so it consumed something", i)
		}
	}

	// The claim then succeeds exactly once.
	if !b.Allow(key) {
		t.Fatal("Allow refused the probe that Permitted said was available")
	}
	if b.Allow(key) {
		t.Error("Allow handed out a second probe while the first was still out")
	}
	if b.Permitted(key) {
		t.Error("Permitted reported true while a probe was out")
	}
}

// TestBreaker_PermittedClosedCircuitAllowsEveryLook covers the ordinary
// state: no failures on record, and a streak a success cleared.
func TestBreaker_PermittedClosedCircuitAllowsEveryLook(t *testing.T) {
	b := breaker.New(3, time.Hour)

	if !b.Permitted(key) {
		t.Error("Permitted refused a target with no history at all")
	}

	b.RecordFailure(key) // one of three, so still closed
	if !b.Permitted(key) {
		t.Error("Permitted refused a target below the failure threshold")
	}

	b.RecordSuccess(key)
	if !b.Permitted(key) {
		t.Error("Permitted refused a target whose streak a success cleared")
	}
}

// TestBreaker_RecordSuccessWithNoHistoryIsANoOp proves a success against
// a target nothing has failed against leaves it closed and allowed.
func TestBreaker_RecordSuccessWithNoHistoryIsANoOp(t *testing.T) {
	b := breaker.New(1, time.Hour)
	b.RecordSuccess(key)
	if !b.Allow(key) {
		t.Fatal("expected a bare success to leave the target allowed")
	}
}

// TestBreaker_ALostProbeIsReissuedAfterItsLease proves a probe nobody
// records an outcome for does not latch the circuit half-open forever.
//
// Every caller in this module is meant to record an outcome, and two of
// them once did not (FAILURE_PATTERNS 146 and 398). Each time the
// result was a target no call could reach again until the process
// restarted. The lease makes that class survivable whichever caller
// loses the probe next.
func TestBreaker_ALostProbeIsReissuedAfterItsLease(t *testing.T) {
	const cooldown = 20 * time.Millisecond
	b := breaker.New(1, cooldown)
	b.RecordFailure(key)

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Allow(key) {
		t.Fatal("expected the first probe after the cooldown")
	}
	// That probe is lost: nothing records its outcome.

	if b.Allow(key) || b.Permitted(key) {
		t.Fatal("expected callers refused while the lost probe's lease still runs")
	}

	time.Sleep(cooldown + 10*time.Millisecond)
	if !b.Permitted(key) {
		t.Fatal("Permitted still refused after the lost probe's lease ran out")
	}
	if !b.Allow(key) {
		t.Fatal("Allow did not reissue the probe after the lost one's lease ran out, so the circuit is latched half-open")
	}
	if b.Allow(key) {
		t.Fatal("expected the reissued probe to be the only one out")
	}

	// The reissued probe resolves the circuit normally.
	b.RecordSuccess(key)
	if !b.Allow(key) {
		t.Fatal("expected the circuit closed after the reissued probe succeeded")
	}
}

// TestBreaker_KeysAreIndependent proves one target's circuit never
// refuses a call to another.
func TestBreaker_KeysAreIndependent(t *testing.T) {
	b := breaker.New(1, time.Hour)
	b.RecordFailure("dead:22")
	if b.Allow("dead:22") {
		t.Fatal("expected the failed target's circuit open")
	}
	if !b.Allow("alive:22") {
		t.Fatal("an open circuit on one target refused a different one")
	}
}

// TestErrOpen_IsTheSharedPhrase pins the sentinel's text, which every
// open-circuit message in this module wraps and which operators and
// existing tests read.
func TestErrOpen_IsTheSharedPhrase(t *testing.T) {
	wrapped := fmt.Errorf("remoteexec: %w for h:22, too many recent failures", breaker.ErrOpen)
	if !errors.Is(wrapped, breaker.ErrOpen) {
		t.Fatal("a wrapped ErrOpen is not recognized by errors.Is")
	}
	if got, want := wrapped.Error(), "remoteexec: circuit open for h:22, too many recent failures"; got != want {
		t.Fatalf("message = %q, want %q", got, want)
	}
}

// TestBreaker_ConcurrentUse hammers every method across many goroutines
// and keys under -race. It asserts only that nothing races or panics;
// the tests above cover the state machine.
func TestBreaker_ConcurrentUse(t *testing.T) {
	b := breaker.New(3, 5*time.Millisecond)
	keys := []string{"a:22", "b:22", "c:22"}

	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 200
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			k := keys[g%len(keys)]
			for i := 0; i < iterations; i++ {
				_ = b.Permitted(k)
				if b.Allow(k) {
					if i%2 == 0 {
						b.RecordSuccess(k)
					} else {
						b.RecordFailure(k)
					}
				}
			}
		}(g)
	}
	wg.Wait()
}
