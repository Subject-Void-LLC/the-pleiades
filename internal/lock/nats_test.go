package lock_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestThunderingHerdLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// 1. Spin up ephemeral NATS container
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

	// 2. Initialize Lock Manager
	mgr, err := lock.NewNatsLockManager(ctx, url)
	if err != nil {
		t.Fatalf("failed to init nats lock manager: %v", err)
	}

	// 3. The Thundering Herd
	// We spawn 100 goroutines that will all attempt to acquire the lock at the exact same moment.
	numRoutines := 100
	var wg sync.WaitGroup
	wg.Add(numRoutines)
	
	// WaitGroup to hold them at the starting gate so they fire simultaneously
	var startingGate sync.WaitGroup
	startingGate.Add(1)

	var successCount int32
	var lockedCount int32
	var unexpectedErrors int32

	targetDevice := "core-router-01"

	for i := 0; i < numRoutines; i++ {
		go func() {
			defer wg.Done()
			startingGate.Wait() // Block until the starting gun fires

			// Attempt lock
			lease, err := mgr.Acquire(context.Background(), targetDevice, 5*time.Second)
			
			if err == nil {
				// We won the race!
				atomic.AddInt32(&successCount, 1)
				
				// Keep the lock for a moment to ensure no one else gets it
				time.Sleep(50 * time.Millisecond)
				
				// Release it (cleanup)
				if err := lease.Release(context.Background()); err != nil {
					t.Errorf("failed to release lock: %v", err)
				}
			} else if err == lock.ErrLockHeld {
				// We lost the race, which is expected for 99 of the routines
				atomic.AddInt32(&lockedCount, 1)
			} else {
				// Some other transport or KV error occurred
				atomic.AddInt32(&unexpectedErrors, 1)
				t.Errorf("unexpected error during acquire: %v", err)
			}
		}()
	}

	// Release the hounds!
	startingGate.Done()

	// Wait for all 100 routines to finish
	wg.Wait()

	// 4. Validate the Release Gate
	if unexpectedErrors > 0 {
		t.Fatalf("Encountered %d unexpected errors during thundering herd", unexpectedErrors)
	}

	if successCount != 1 {
		t.Fatalf("Expected exactly 1 routine to acquire the lock, but %d succeeded. SPLIT BRAIN DETECTED!", successCount)
	}

	if lockedCount != 99 {
		t.Fatalf("Expected exactly 99 routines to hit ErrLockHeld, got %d", lockedCount)
	}

	t.Log("Thundering Herd Test Passed! Lock exclusivity is perfectly maintained under massive concurrency.")
}
