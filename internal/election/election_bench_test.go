package election_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

// electionTTLForBench must match internal/election's own unexported
// electionTTL constant. It cannot be imported directly (unexported), so
// it is restated here; if the package's tuned TTL ever changes, this
// benchmark's own request should be updated alongside it so it keeps
// measuring the same renewal shape Run itself performs in production.
const electionTTLForBench = 2 * time.Second

// BenchmarkLeaderElectorKeepAlive measures the exact latency of the
// KeepAlive call LeaderElector.Run uses to renew its lease every
// electionInterval while it holds leadership, against a real NATS
// JetStream KV store. This is this package's migration of
// engine.Scheduler's own BenchmarkSchedulerKeepAlive, at this phase's
// retuned TTL.
//
// Container teardown is registered via b.Cleanup, not a naked defer,
// mirroring internal/lock/nats_bench_test.go's own startBenchNats
// helper: a real, test-caught measurement defect surfaced while writing
// this benchmark (recorded in LESSONS_LEARNED.md) when this was first
// written with defer natsContainer.Terminate/defer mgr.Close instead. go
// test -bench measures elapsed wall time from ResetTimer to this
// function's own return, and a deferred call runs during that same
// return, before the function is considered finished -- so several
// seconds of real container teardown were being divided by b.N and
// added to every reported op, inflating it 100-1000x above the true
// per-call cost. b.Cleanup-registered functions run strictly after the
// benchmark's own measurement completes, closing this for good rather
// than requiring every caller to remember an explicit b.StopTimer.
//
// Reference platform: Kubernetes' own client-go leaderelection package
// (k8s.io/client-go/tools/leaderelection), the industry-standard leader
// election implementation most operators compare against, defaults to
// DefaultLeaseDuration=15s / DefaultRenewDeadline=10s /
// DefaultRetryPeriod=2s (published, not locally measured; verified
// against the current github.com/kubernetes/client-go source before
// citing here, not assumed from memory). client-go's own defaults tune
// for a much larger worst-case failover window (order of 15s) than this
// package's own ~2.5s target (electionTTL=2s + electionInterval=500ms);
// the real KeepAlive latency measured below is the evidence that a much
// tighter window is achievable on this stack without the renewal call
// itself becoming the bottleneck.
func BenchmarkLeaderElectorKeepAlive(b *testing.B) {
	ctx := context.Background()

	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage(testsupport.NATSImage),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready").WithStartupTimeout(testsupport.ContainerStartupTimeout)),
	)
	if err != nil {
		b.Fatalf("failed to start container: %v", err)
	}
	b.Cleanup(func() { _ = natsContainer.Terminate(ctx) })

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		b.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url, nil)
	if err != nil {
		b.Fatalf("failed to init nats lock manager: %v", err)
	}
	b.Cleanup(func() { _ = mgr.Close() })

	lease, err := mgr.Acquire(ctx, "bench-election-key", electionTTLForBench, lock.AcquireOptions{})
	if err != nil {
		b.Fatalf("failed to acquire initial lease: %v", err)
	}

	// A handful of untimed calls first, so the timed loop below measures
	// steady-state renewal cost rather than any one-time cost of this
	// specific connection's very first request.
	for i := 0; i < 5; i++ {
		if err := lease.KeepAlive(ctx); err != nil {
			b.Fatalf("failed to warm up: %v", err)
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := lease.KeepAlive(ctx); err != nil {
			b.Fatalf("failed to keep alive: %v", err)
		}
	}
}
