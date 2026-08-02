package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
)

// mockLockManager implements lock.Manager for fast fuzzing/benchmarking
type mockLockManager struct{}

func (m *mockLockManager) Acquire(ctx context.Context, itemID string, ttl time.Duration) (lock.Lease, error) {
	return &mockLease{}, nil
}

func (m *mockLockManager) Close() error {
	return nil
}

type mockLease struct{}

func (m *mockLease) ID() string { return "mock" }
func (m *mockLease) KeepAlive(ctx context.Context) error { return nil }
func (m *mockLease) Release(ctx context.Context) error { return nil }

func FuzzSchedulerCancellation(f *testing.F) {
	f.Add(10)
	f.Add(500)
	f.Add(2000)
	
	f.Fuzz(func(t *testing.T, cancelDelayMs int) {
		if cancelDelayMs < 0 || cancelDelayMs > 5000 {
			return
		}

		mgr := &mockLockManager{}
		s := engine.NewScheduler(mgr)
		
		ctx, cancel := context.WithCancel(context.Background())
		
		// Run in background
		go s.Run(ctx)
		
		// Wait a fuzzed amount of time, then cancel
		time.Sleep(time.Duration(cancelDelayMs) * time.Millisecond)
		cancel()
		
		// The fuzz test ensures that regardless of when we pull the plug (during wait, during acquire, etc),
		// it doesn't panic.
	})
}
