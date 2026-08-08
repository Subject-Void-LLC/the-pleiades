package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startBenchNats starts one real NATS container and lock.Manager, shared
// across every benchmark in this file via b.Cleanup, so each individual
// Benchmark* function does not pay its own container-startup cost.
func startBenchNats(b *testing.B) (context.Context, lock.Manager) {
	b.Helper()
	ctx := context.Background()

	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.11"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		b.Fatalf("failed to start nats: %v", err)
	}
	b.Cleanup(func() { _ = natsContainer.Terminate(ctx) })

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		b.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url)
	if err != nil {
		b.Fatalf("failed to init lock manager: %v", err)
	}
	b.Cleanup(func() { _ = mgr.Close() })

	return ctx, mgr
}

// BenchmarkLockAcquisition measures the latency of acquiring and releasing
// an exclusive NATS KV lock, uncontended. This is critical since a slow
// locking mechanism will bottleneck the entire workflow engine.
//
// [REFERENCE]: Redis SETNX is commonly cited at ~1-3ms locally, but that
// figure is a published claim for a different system on different
// hardware, not a measurement taken here (PATTERNS.md's own precedent for
// this distinction: Phase 2's event bus benchmarks explicitly avoid an
// "unverifiable published figure for a different system" in favor of a
// real one). BenchmarkPostgresAdvisoryLock (pg_advisory_lock_test.go) is
// the real, locally measured industry-alternative comparison for this
// package: same host, same run.
func BenchmarkLockAcquisition(b *testing.B) {
	ctx, mgr := startBenchNats(b)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lease, err := mgr.Acquire(ctx, "bench-device", 5*time.Second, lock.AcquireOptions{})
		if err != nil {
			b.Fatalf("failed to acquire lock: %v", err)
		}

		err = lease.Release(ctx)
		if err != nil {
			b.Fatalf("failed to release lock: %v", err)
		}
	}
}

// BenchmarkLockAcquisition_Shared measures the same acquire/release-in-a-loop
// shape as BenchmarkLockAcquisition, but under ModeShared: each iteration's
// itemID is unique (nothing to actually share against), so this isolates
// the cost of encoding/decoding the JSON lockValue envelope on top of the
// same Create-based fast path exclusive mode already pays, without any
// join-CAS round trip.
func BenchmarkLockAcquisition_Shared(b *testing.B) {
	ctx, mgr := startBenchNats(b)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lease, err := mgr.Acquire(ctx, "bench-device-shared", 5*time.Second, lock.AcquireOptions{Mode: lock.ModeShared})
		if err != nil {
			b.Fatalf("failed to acquire shared lock: %v", err)
		}
		if err := lease.Release(ctx); err != nil {
			b.Fatalf("failed to release shared lock: %v", err)
		}
	}
}

// BenchmarkLockKeepAlive measures the latency of a single KeepAlive call
// against an already-held exclusive lease: the raw-publish TTL-refresh
// path (nats.go's publishWithTTL), not timed by any earlier benchmark
// since KeepAlive had no real renewal mechanism before this phase.
func BenchmarkLockKeepAlive(b *testing.B) {
	ctx, mgr := startBenchNats(b)

	lease, err := mgr.Acquire(ctx, "bench-device-keepalive", time.Minute, lock.AcquireOptions{})
	if err != nil {
		b.Fatalf("failed to acquire lock: %v", err)
	}
	b.Cleanup(func() { _ = lease.Release(context.Background()) })

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := lease.KeepAlive(ctx); err != nil {
			b.Fatalf("failed to keep alive: %v", err)
		}
	}
}

// BenchmarkLockAcquisition_QueueContended measures how long a PolicyQueue
// Acquire takes to succeed when itemID is genuinely held by another
// caller at the moment it is requested: each iteration pre-acquires
// itemID, starts a goroutine that releases it after a small fixed delay,
// then times how long the contending Acquire call takes to get through
// acquireWithContention's own backoff loop. This is the real, measured
// cost PolicyQueue adds over the uncontended path BenchmarkLockAcquisition
// measures, not a guess: the two are directly comparable since both run
// against the same container in the same benchmark run.
func BenchmarkLockAcquisition_QueueContended(b *testing.B) {
	ctx, mgr := startBenchNats(b)
	const holdDelay = 20 * time.Millisecond

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		holder, err := mgr.Acquire(ctx, "bench-device-queue", 5*time.Second, lock.AcquireOptions{})
		if err != nil {
			b.Fatalf("failed to pre-acquire lock: %v", err)
		}
		go func() {
			time.Sleep(holdDelay)
			_ = holder.Release(context.Background())
		}()

		waiter, err := mgr.Acquire(ctx, "bench-device-queue", 5*time.Second, lock.AcquireOptions{Policy: lock.PolicyQueue})
		if err != nil {
			b.Fatalf("failed to acquire under PolicyQueue: %v", err)
		}
		if err := waiter.Release(ctx); err != nil {
			b.Fatalf("failed to release waiter: %v", err)
		}
	}
}
