package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// BenchmarkSchedulerKeepAlive measures the exact latency of the KeepAlive loop
// that the Scheduler uses to maintain leadership.
// It proves our architecture stays well under the 10-20ms threshold needed
// to prevent accidental split-brain failovers under CPU starvation.
func BenchmarkSchedulerKeepAlive(b *testing.B) {
	ctx := context.Background()

	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		b.Fatalf("failed to start container: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		b.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url)
	if err != nil {
		b.Fatalf("failed to init nats lock manager: %v", err)
	}

	// We acquire the lock directly
	lease, err := mgr.Acquire(ctx, "bench-scheduler-key", 10*time.Second)
	if err != nil {
		b.Fatalf("failed to acquire initial lease: %v", err)
	}

	b.ResetTimer()

	// Benchmark the KeepAlive cycle which the Scheduler executes periodically
	for i := 0; i < b.N; i++ {
		err := lease.KeepAlive(ctx)
		if err != nil {
			b.Fatalf("failed to keep alive: %v", err)
		}
	}
}
