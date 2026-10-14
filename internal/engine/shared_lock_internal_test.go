// Tests that a shared device's tasks take a shared lock, which runs hold
// at once, while every other device's stay exclusive.
package engine

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// lockDevice is a device that is, or is not, shared across runs. Every
// method a lock decision does not call stays on the nil embedded interface.
type lockDevice struct {
	inventory.InventoryItem
	shared bool
}

func (d lockDevice) SharedAcrossRuns() bool { return d.shared }

// TestDeviceLock_ASharedDeviceIsHeldByTwoRunsAtOnce takes two leases on one
// device through the options a task's lock uses, against the in-process
// lock manager: a shared device's second lease is granted while the first
// is held, and a machine's waits.
func TestDeviceLock_ASharedDeviceIsHeldByTwoRunsAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		device lockDevice
		both   bool
	}{
		{"a cluster", lockDevice{shared: true}, true},
		{"a machine", lockDevice{shared: false}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := lock.NewInProcessManager()
			first, err := mgr.Acquire(context.Background(), "dev", time.Minute, deviceLockOptions(tc.device))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = first.Release(context.Background()) }()

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			second, err := mgr.Acquire(ctx, "dev", time.Minute, deviceLockOptions(tc.device))
			if got := err == nil; got != tc.both {
				t.Fatalf("a second lease while the first is held: granted=%v (%v), want granted=%v", got, err, tc.both)
			}
			if second != nil {
				_ = second.Release(context.Background())
			}
		})
	}
}

// TestDeviceLockOptions_OnlyASharedDeviceIsShared checks the decision:
// shared for a device that says it is, exclusive for one that says it is
// not and for one that says nothing.
func TestDeviceLockOptions_OnlyASharedDeviceIsShared(t *testing.T) {
	if deviceLockOptions(lockDevice{shared: true}).Mode != lock.ModeShared {
		t.Error("a shared device's lock is not shared")
	}
	if deviceLockOptions(lockDevice{shared: false}).Mode != lock.ModeExclusive {
		t.Error("a device that is not shared took a shared lock")
	}
	var plain struct{ inventory.InventoryItem }
	if deviceLockOptions(plain).Mode != lock.ModeExclusive {
		t.Error("a device that says nothing took a shared lock")
	}
}
