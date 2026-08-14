// Tests for the derivation gate.
//
// The gate is the only thing standing between an unauthenticated endpoint
// and an unbounded number of ~20 MB allocations, so the property that
// matters is the ceiling, not the throughput.
package localauth_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
)

func TestGate_NeverExceedsItsLimit(t *testing.T) {
	const (
		limit   = 3
		callers = 50
	)
	gate := localauth.NewGate(limit)

	var (
		inFlight atomic.Int64
		peak     atomic.Int64
		wg       sync.WaitGroup
	)

	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = gate.Do(context.Background(), func() {
				current := inFlight.Add(1)
				// Record the high-water mark rather than asserting inside
				// the critical section, so a violation is reported once at
				// the end instead of racing to fail the test N times.
				for {
					seen := peak.Load()
					if current <= seen || peak.CompareAndSwap(seen, current) {
						break
					}
				}
				// Long enough that concurrent callers genuinely overlap; a
				// gate that admitted everyone would show it here.
				time.Sleep(2 * time.Millisecond)
				inFlight.Add(-1)
			})
		}()
	}
	wg.Wait()

	if got := peak.Load(); got > limit {
		t.Errorf("peak concurrency = %d, want at most %d; the gate admitted more derivations than it allows, "+
			"which at ~20 MB each is the memory exhaustion it exists to prevent", got, limit)
	}
	if got := peak.Load(); got < 2 {
		t.Errorf("peak concurrency = %d; the test never actually overlapped, so it proved nothing", got)
	}
	if got := inFlight.Load(); got != 0 {
		t.Errorf("in-flight count = %d after every caller returned, want 0; a slot leaked", got)
	}
}

func TestGate_AllCallersEventuallyRun(t *testing.T) {
	// A gate that dropped work rather than queueing it would be a login
	// endpoint that fails under mild load.
	gate := localauth.NewGate(2)

	var ran atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := gate.Do(context.Background(), func() { ran.Add(1) }); err != nil {
				t.Errorf("Do() error = %v with no deadline set", err)
			}
		}()
	}
	wg.Wait()

	if got := ran.Load(); got != 20 {
		t.Errorf("%d of 20 callers ran, want all of them", got)
	}
}

func TestGate_RespectsAnExpiredContext(t *testing.T) {
	// A caller whose client already hung up must stop waiting rather than
	// hold a slot for a response nobody will read. Without this, abandoned
	// requests keep the gate saturated against the legitimate ones behind
	// them.
	gate := localauth.NewGate(1)

	release := make(chan struct{})
	occupied := make(chan struct{})
	go func() {
		_ = gate.Do(context.Background(), func() {
			close(occupied)
			<-release
		})
	}()
	<-occupied
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	var ran bool
	err := gate.Do(ctx, func() { ran = true })

	if !errors.Is(err, localauth.ErrBusy) {
		t.Errorf("Do() error = %v, want ErrBusy", err)
	}
	if ran {
		t.Error("the function ran despite the gate being full and the context expiring")
	}
}

func TestNewGate_TreatsAnUnsetLimitAsOneRatherThanUnlimited(t *testing.T) {
	// Unlimited is the configuration this type exists to prevent, so it
	// must not be reachable by typing a zero into a config file.
	for _, n := range []int{0, -1, -1000} {
		gate := localauth.NewGate(n)

		blocked := make(chan struct{})
		go func() {
			_ = gate.Do(context.Background(), func() { <-blocked })
		}()

		// Give the goroutine above time to take the only slot, then prove a
		// second caller cannot get in.
		time.Sleep(20 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := gate.Do(ctx, func() {})
		cancel()
		close(blocked)

		if !errors.Is(err, localauth.ErrBusy) {
			t.Errorf("NewGate(%d) admitted a second caller: error = %v, want ErrBusy", n, err)
		}
	}
}

func TestGate_ReleasesItsSlotWhenTheFunctionPanics(t *testing.T) {
	// A panicking derivation must not permanently consume a slot, or one
	// bad row would shrink the gate for the life of the process.
	gate := localauth.NewGate(1)

	func() {
		defer func() { _ = recover() }()
		_ = gate.Do(context.Background(), func() { panic("boom") })
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := gate.Do(ctx, func() {}); err != nil {
		t.Errorf("Do() error = %v after a panic; the slot leaked", err)
	}
}
