// Package rowlocktest holds the scenario that proves a store's fold update
// runs under its row's write lock: the inventory set store's and the
// announcement store's UpdateWith, on SQLite in their own packages' tests and
// on PostgreSQL in this package's.
//
// The scenario holds one update inside its fold and starts a second. Under
// the lock the second update's fold cannot run until the first has
// committed, so it folds over what the first wrote. Without it, the second
// reads the row as it was and writes that back over the first's change.
package rowlocktest

import (
	"sync/atomic"
	"testing"
	"time"
)

// Update is one fold update of a store. It calls onFold first thing inside
// its fold and returns the update's error.
type Update func(onFold func()) error

// settle is how long the second update is given to reach its fold before the
// first is released. A slow start only makes the scenario pass without
// proving the lock, never fail with it.
const settle = 200 * time.Millisecond

// SecondFoldWaits runs first with its fold held open, starts second, and
// releases first after settle. It fails t when either update fails or when
// second's fold ran before first was released, and returns only once both
// have finished, so the caller can then check what second's fold saw and
// what was saved.
func SecondFoldWaits(t *testing.T, first, second Update) {
	t.Helper()

	inside, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first(func() {
			close(inside)
			<-release
		})
	}()
	// A first update that fails before its fold would leave inside open
	// forever; report its error rather than waiting for the package's
	// timeout to hide it.
	select {
	case <-inside:
	case err := <-firstDone:
		t.Fatalf("first update ended before its fold ran: %v", err)
	}

	var released, foldedEarly atomic.Bool
	started := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(started)
		secondDone <- second(func() {
			if !released.Load() {
				foldedEarly.Store(true)
			}
		})
	}()

	// The second update has at least been called before the clock starts.
	<-started
	time.Sleep(settle)
	released.Store(true)
	close(release)
	if err := <-firstDone; err != nil {
		t.Errorf("first update: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Errorf("second update: %v", err)
	}
	if foldedEarly.Load() {
		t.Error("the second update ran its fold while the first held the row's lock")
	}
}
