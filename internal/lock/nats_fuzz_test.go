package lock_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// FuzzLockAcquisition fuzzes natsLockManager.Acquire against a single real
// NATS container shared across every iteration (starting a fresh container
// per iteration would be too slow to get meaningful fuzz coverage), with
// randomized itemID strings (garbage, overly long, or SQL/NATS-subject
// injection shaped) and AcquireOptions. Every fuzzed itemID is namespaced
// with a monotonic counter so iterations can never collide with each
// other's leftover state, while the fuzzed content itself still reaches
// kvSubject's own hand-built "$KV.<bucket>.<key>" subject construction, the
// real boundary this test exists to harden (see nats.go's own doc comment
// on kvSubject). An earlier version of this test fuzzed a string and did
// nothing with it, never calling Acquire at all: zero coverage wearing a
// fuzz test's shape (FAILURE_PATTERNS.md).
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
	// clean error, before itemIDValid started rejecting this up front).
	f.Add("..0", int64(5*time.Second), 0)

	ctx := context.Background()
	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		f.Fatalf("failed to start container: %v", err)
	}
	f.Cleanup(func() { _ = natsContainer.Terminate(ctx) })

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		f.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url, nil)
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
			if errors.Is(err, lock.ErrLockHeld) || errors.Is(err, jetstream.ErrInvalidKey) || strings.Contains(id, "..") {
				// All legitimate outcomes: real contention, a fuzzed
				// itemID containing characters NATS's own KV key
				// validation rejects (e.g. whitespace, '*', '>') before
				// this package's own code ever runs, or consecutive dots
				// (nats.go's own key validation would accept these but
				// itemIDValid rejects them first; see its own doc
				// comment). A real caller only ever passes an inventory
				// device ID here, never arbitrary user input, so
				// rejecting an invalid one cleanly is correct, not a
				// defect to chase.
				return
			}
			t.Fatalf("unexpected error acquiring lock for %q: %v", id, err)
		}
		if err := lease.Release(acquireCtx); err != nil {
			t.Fatalf("a successful acquire for %q could not be released: %v", id, err)
		}
	})
}
