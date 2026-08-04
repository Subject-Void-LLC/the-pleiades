package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
)

// BenchmarkInProcessLockAcquisition measures the latency of acquiring and
// releasing an in-process lock, in the same acquire/release-in-a-loop
// shape as BenchmarkLockAcquisition in nats_bench_test.go. Because there
// is no network hop, this is expected to land orders of magnitude below
// that benchmark's own ~1ms-3ms Redis SETNX reference figure.
func BenchmarkInProcessLockAcquisition(b *testing.B) {
	ctx := context.Background()
	mgr := lock.NewInProcessManager()

	b.ResetTimer()
	start := time.Now() // measured independently of b.N so we can log a real per-op figure below

	for i := 0; i < b.N; i++ {
		lease, err := mgr.Acquire(ctx, "bench-device", 5*time.Second, lock.AcquireOptions{})
		if err != nil {
			b.Fatalf("failed to acquire lock: %v", err)
		}

		if err := lease.Release(ctx); err != nil {
			b.Fatalf("failed to release lock: %v", err)
		}
	}

	elapsed := time.Since(start)
	perOp := elapsed / time.Duration(b.N)

	// [REFERENCE] compares this run's measured per-operation latency
	// against BenchmarkLockAcquisition's own measured NATS figure and
	// BenchmarkPostgresAdvisoryLock's own measured Postgres figure (both
	// in this same package). In-process pays no network round trip and no
	// JetStream KV revision bookkeeping, so it should land orders of
	// magnitude below both.
	b.Logf("[REFERENCE] in-process lock acquire+release: %v/op over %d ops", perOp, b.N)
}

// BenchmarkInProcessLockAcquisition_Shared is the in-process counterpart to
// nats_bench_test.go's BenchmarkLockAcquisition_Shared: each iteration's
// itemID is unique, isolating the cost of the holders-map bookkeeping
// ModeShared adds over exclusive mode, without any real contention.
func BenchmarkInProcessLockAcquisition_Shared(b *testing.B) {
	ctx := context.Background()
	mgr := lock.NewInProcessManager()

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

// BenchmarkInProcessLockKeepAlive is the in-process counterpart to
// nats_bench_test.go's BenchmarkLockKeepAlive.
func BenchmarkInProcessLockKeepAlive(b *testing.B) {
	ctx := context.Background()
	mgr := lock.NewInProcessManager()

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
