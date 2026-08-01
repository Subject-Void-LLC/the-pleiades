package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// BenchmarkLockAcquisition measures the latency of acquiring and releasing a NATS KV lock.
// This is critical since a slow locking mechanism will bottleneck the entire workflow engine.
// Reference: Redis SETNX typically takes 1-3ms locally.
func BenchmarkLockAcquisition(b *testing.B) {
	ctx := context.Background()

	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		b.Fatalf("failed to start nats: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		b.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url)
	if err != nil {
		b.Fatalf("failed to init lock manager: %v", err)
	}

	b.Logf("[REFERENCE] Redis SETNX distributed lock locally: ~1ms - 3ms")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lease, err := mgr.Acquire(ctx, "bench-device", 5*time.Second)
		if err != nil {
			b.Fatalf("failed to acquire lock: %v", err)
		}
		
		err = lease.Release(ctx)
		if err != nil {
			b.Fatalf("failed to release lock: %v", err)
		}
	}
}
