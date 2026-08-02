package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/goleak"
)

func TestSchedulerLeaderElection(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Bulletproof Matrix: Verify ZERO goroutine leaks when test ends
	defer goleak.VerifyNone(t)

	ctx := context.Background()

	natsContainer, err := nats.RunContainer(ctx,
		testcontainers.WithImage("nats:2.10"),
		testcontainers.WithCmd("-js"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready")),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
	defer natsContainer.Terminate(ctx)

	url, err := natsContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	mgr, err := lock.NewNatsLockManager(ctx, url)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}
	defer mgr.Close()

	// Create 3 replicas
	s1 := engine.NewScheduler(mgr)
	s2 := engine.NewScheduler(mgr)
	s3 := engine.NewScheduler(mgr)

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	ctx3, cancel3 := context.WithCancel(context.Background())

	// Start all three concurrently
	go s1.Run(ctx1)
	go s2.Run(ctx2)
	go s3.Run(ctx3)

	// Wait for one to become the leader
	time.Sleep(3 * time.Second)

	leaders := 0
	var leaderCancel context.CancelFunc

	if s1.IsLeader() {
		leaders++
		leaderCancel = cancel1
	}
	if s2.IsLeader() {
		leaders++
		leaderCancel = cancel2
	}
	if s3.IsLeader() {
		leaders++
		leaderCancel = cancel3
	}

	if leaders != 1 {
		t.Fatalf("Expected exactly 1 leader, got %d. SPLIT BRAIN DETECTED!", leaders)
	}
	t.Log("Replica correctly acquired initial leadership.")

	// GRACEFUL HANDOVER TEST: Kill the leader
	t.Log("Simulating K8s Pod Death (SIGTERM) on the leader...")
	leaderCancel()

	// Wait for another node to take over. Because we implemented graceful handover (Release on ctx.Done),
	// this should happen on the very next tick (2 seconds).
	time.Sleep(3 * time.Second)

	leaders = 0
	if s1.IsLeader() {
		leaders++
	}
	if s2.IsLeader() {
		leaders++
	}
	if s3.IsLeader() {
		leaders++
	}

	if leaders != 1 {
		t.Fatalf("Expected exactly 1 new leader after failover, got %d. ELECTION FAILED!", leaders)
	}
	t.Log("Graceful handover and failover successful.")

	// Cleanup all remaining goroutines to satisfy goleak
	cancel1()
	cancel2()
	cancel3()
	
	// Wait a bit for goroutines to fully return before goleak checks
	time.Sleep(500 * time.Millisecond)
}
