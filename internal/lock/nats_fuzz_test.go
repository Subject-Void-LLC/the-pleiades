package lock_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// FuzzLockAcquisition fuzzes natsLockManager.Acquire against a single real
// NATS container shared across every iteration (starting a fresh container
// per iteration would be too slow to get meaningful fuzz coverage), with
// randomized itemID strings (garbage, overly long, or SQL/NATS-subject
// injection shaped) and AcquireOptions. Every fuzzed itemID is namespaced
// with a monotonic counter so iterations can never collide with each
// other's leftover state, while the fuzzed content itself still reaches
// kvKey's encoding and, through it, kvSubject's own hand-built
// "$KV.<bucket>.<key>" subject construction, the real boundary this test
// exists to harden (see nats.go's own doc comments on kvKey and
// kvSubject). Since FAILURE_PATTERNS.md #206's fix, the property asserted
// is total: EVERY itemID must acquire and release cleanly, because kvKey
// encodes it to a single legal subject token before the client ever sees
// it. There is deliberately no allowance for a validation error any more;
// the old allowances (nats.go's ErrInvalidKey, itemIDValid's ".."
// rejection) described pre-encoder behavior, and keeping them would let a
// regression in the encoder hide inside a tolerated branch. An earlier
// version of this test fuzzed a string and did nothing with it, never
// calling Acquire at all: zero coverage wearing a fuzz test's shape
// (FAILURE_PATTERNS.md).
func FuzzLockAcquisition(f *testing.F) {
	if testing.Short() {
		f.Skip("skipping integration fuzz target in short mode")
	}

	f.Add("device-1", int64(5*time.Second), 0)
	f.Add("", int64(0), 0)
	f.Add(strings.Repeat("long-id-", 100), int64(time.Hour), 1)
	f.Add("'; DROP TABLE devices; --", int64(5*time.Second), 0)
	f.Add("$KV.Pleiades_Locks.other-key", int64(5*time.Second), 1)
	f.Add("\x00\x01\xff unicode-ish/../../etc", int64(5*time.Second), 0)
	// Regression pin: consecutive dots pass nats.go's own key validation
	// but produce an empty NATS subject token, which the server never
	// acknowledges at all (a real, fuzz-caught multi-second timeout, not a
	// clean error, in the pre-encoder code that interpolated the raw
	// itemID into the subject). kvKey's encoding must turn this into an
	// ordinary clean acquire, not a timeout and not a rejection.
	f.Add("..0", int64(5*time.Second), 0)
	// The itemID shape FAILURE_PATTERNS.md #206 is about: dots are legal
	// in a KV key, so this used to store a five-token subject where three
	// were intended, which a per-device grant could never name.
	f.Add("router1.example.com", int64(5*time.Second), 0)

	ctx := context.Background()
	url := testsupport.StartNATS(f).URL()

	mgr, err := lock.NewNatsLockManager(ctx, url, nil, topology.StreamProvisioner)
	if err != nil {
		f.Fatalf("failed to init nats lock manager: %v", err)
	}
	f.Cleanup(func() { _ = mgr.Close() })

	var counter int64

	// minPositiveTTL mirrors nats.go's own unexported constant of the same
	// name (real per-key TTL requires whole-second server-side
	// granularity); duplicated here as a literal, not imported, since this
	// is an external _test package and the value is small and stable.
	const minPositiveTTL = time.Second

	f.Fuzz(func(t *testing.T, itemID string, ttlNanos int64, modeSeed int) {
		const maxTTL = int64(time.Hour)
		if ttlNanos > maxTTL {
			ttlNanos = maxTTL
		}
		if ttlNanos < 0 {
			ttlNanos = 0
		}
		ttl := time.Duration(ttlNanos)

		// Namespaced so this iteration can never collide with a previous
		// one's leftover state, while still routing the fuzzed content
		// through kvSubject's own subject construction.
		id := fmt.Sprintf("fuzz-%d-%s", atomic.AddInt64(&counter, 1), itemID)
		opts := lock.AcquireOptions{Mode: lock.Mode(mod(modeSeed, 2))}

		acquireCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		lease, err := mgr.Acquire(acquireCtx, id, ttl, opts)
		if ttl > 0 && ttl < minPositiveTTL {
			// A real, deliberate rejection (nats.go's own minPositiveTTL
			// check), not a defect: assert only that it is a clean Go
			// error, never that Acquire actually succeeded.
			if err == nil {
				_ = lease.Release(acquireCtx)
				t.Fatalf("expected an error acquiring with sub-second positive ttl %v, got nil", ttl)
			}
			return
		}
		if err != nil {
			// No allowances: the counter makes every id unique (so
			// ErrLockHeld is impossible), and kvKey makes every id legal
			// (so the client's own key validation can never fire). Any
			// error here is a real defect.
			t.Fatalf("unexpected error acquiring lock for %q: %v", id, err)
		}
		if err := lease.Release(acquireCtx); err != nil {
			t.Fatalf("a successful acquire for %q could not be released: %v", id, err)
		}
	})
}
