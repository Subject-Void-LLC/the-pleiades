package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/customerx/pleiades/internal/lock"
)

// mockLease implements lock.Lease
type mockLease struct {
	id string
}

func (m *mockLease) ID() string {
	return m.id
}

func (m *mockLease) KeepAlive(ctx context.Context) error {
	return nil
}

func (m *mockLease) Release(ctx context.Context) error {
	return nil
}

// mockManager implements lock.Manager
type mockManager struct{}

func (m *mockManager) Acquire(ctx context.Context, itemID string, ttl time.Duration) (lock.Lease, error) {
	return &mockLease{id: "lock-1"}, nil
}

func TestLockManagerCompliance(t *testing.T) {
	// If mockManager does not implement lock.Manager, the compiler will fail.
	var mgr lock.Manager = &mockManager{}
	
	lease, err := mgr.Acquire(context.Background(), "device-1", 10*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if lease.ID() != "lock-1" {
		t.Errorf("expected lease ID 'lock-1', got '%s'", lease.ID())
	}
}
