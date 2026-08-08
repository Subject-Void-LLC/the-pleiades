package lock_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// FuzzInProcessManager fuzzes Acquire against a fresh inProcessManager with
// randomized itemID strings, ttl durations, and every AcquireOptions field
// (Mode, Policy, Priority). It asserts that nothing panics, that a negative
// ttl is always rejected with a clear error (never silently accepted, and
// never allowed to reach time.Duration arithmetic with a pathological
// magnitude), and that a non-negative ttl on a fresh manager (which can
// never contend, since the manager and itemID are both new every iteration,
// so every AcquireOptions combination is expected to succeed identically)
// can always be Released without error, which is the fundamental promise a
// caller relies on regardless of which Mode/Policy it acquired under.
func FuzzInProcessManager(f *testing.F) {
	f.Add("device-1", int64(5*time.Second), 0, 0, 0)
	f.Add("", int64(0), 1, 0, 0)
	f.Add(strings.Repeat("long-id-", 100), int64(time.Hour), 0, 1, 5)
	f.Add("'; DROP TABLE devices; --", int64(-1), 0, 2, -3)
	f.Add("\x00\x01\xff unicode-ish", int64(-5*time.Second), 1, 2, 100)

	f.Fuzz(func(t *testing.T, itemID string, ttlNanos int64, modeSeed, policySeed, priority int) {
		// Clamp to a sane range so the fuzzer cannot hand time.Duration
		// arithmetic a pathological magnitude. Behavior at that extreme
		// is not the property under test; panic-freedom and the
		// acquire-then-release promise are.
		const maxTTL = int64(time.Hour)
		if ttlNanos > maxTTL {
			ttlNanos = maxTTL
		}
		if ttlNanos < -maxTTL {
			ttlNanos = -maxTTL
		}
		ttl := time.Duration(ttlNanos)

		opts := lock.AcquireOptions{
			Mode:     lock.Mode(mod(modeSeed, 2)),
			Policy:   lock.ContentionPolicy(mod(policySeed, 3)),
			Priority: priority,
		}

		// A fresh manager per iteration means itemID collisions across
		// iterations cannot cause a spurious ErrLockHeld, and a brand new
		// itemID can never contend against anything already held, so
		// every opts combination is expected to behave identically here.
		mgr := lock.NewInProcessManager()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		lease, err := mgr.Acquire(ctx, itemID, ttl, opts)
		if ttl < 0 {
			if err == nil {
				t.Fatalf("expected an error acquiring with negative ttl %v, got nil", ttl)
			}
			return
		}
		if err != nil {
			t.Fatalf("unexpected error acquiring lock for %q with ttl %v opts %+v: %v", itemID, ttl, opts, err)
		}

		if err := lease.Release(context.Background()); err != nil {
			t.Fatalf("a successful acquire for %q could not be released: %v", itemID, err)
		}
	})
}

// mod returns x modulo n, always in [0, n), unlike Go's % operator, which
// keeps the sign of x for a negative x.
func mod(x, n int) int {
	return ((x % n) + n) % n
}
