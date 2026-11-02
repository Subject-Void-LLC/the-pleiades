// Package lock_test: every adapter's leases carry a fencing token that orders
// their acquisitions on one item (Phase 40a), for every adapter alike.
package lock_test

import (
	"context"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// conformanceFencingTokensOrderAcquisitions: a lease's token is not zero, a
// KeepAlive leaves it as it was, a lease taken after another one on the same
// item was released holds a larger token, and so does a shared holder that
// joins after another.
func conformanceFencingTokensOrderAcquisitions(t *testing.T, mgr lock.Manager) {
	ctx := context.Background()
	const ttl = 5 * time.Second
	var epoch string
	token := func(t *testing.T, lease lock.Lease) uint64 {
		t.Helper()
		got, gotEpoch, ok := lock.FencingToken(lease)
		if !ok {
			t.Fatalf("a %T carries no fencing token", lease)
		}
		// One manager's leases share one epoch, so their tokens compare.
		if epoch == "" {
			epoch = gotEpoch
		} else if gotEpoch != epoch {
			t.Errorf("one manager's leases hold epochs %q and %q", epoch, gotEpoch)
		}
		return got
	}

	item := "fence-" + t.Name()
	first, err := mgr.Acquire(ctx, item, ttl, lock.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := token(t, first)
	if err := first.KeepAlive(ctx); err != nil {
		t.Fatal(err)
	}
	if after := token(t, first); after != before {
		t.Errorf("KeepAlive moved the fencing token from %d to %d; it names the acquisition", before, after)
	}
	if err := first.Release(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := mgr.Acquire(ctx, item, ttl, lock.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := token(t, second); got <= before {
		t.Errorf("a lease taken after the first was released holds token %d, not more than the first's %d", got, before)
	}
	if err := second.Release(ctx); err != nil {
		t.Fatal(err)
	}

	shared := "fence-shared-" + t.Name()
	a, err := mgr.Acquire(ctx, shared, ttl, lock.AcquireOptions{Mode: lock.ModeShared})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Release(ctx) }()
	b, err := mgr.Acquire(ctx, shared, ttl, lock.AcquireOptions{Mode: lock.ModeShared})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Release(ctx) }()
	if token(t, b) <= token(t, a) {
		t.Errorf("a shared holder that joined later holds token %d, not more than %d", token(t, b), token(t, a))
	}
}

// TestFence_CarriesATokenUntilLost: the context carries a Fence, whose token
// stands until the lease is lost and never comes back; none, and a zero
// token, report no token.
func TestFence_CarriesATokenUntilLost(t *testing.T) {
	if _, _, ok := lock.FenceFrom(context.Background()).Token(); ok {
		t.Error("a bare context reported a fencing token")
	}
	fence := lock.NewFence(42, "e1")
	if got, epoch, ok := lock.FenceFrom(lock.WithFence(context.Background(), fence)).Token(); !ok || got != 42 || epoch != "e1" {
		t.Errorf("Token = %d, %q, %v; want 42, e1, true", got, epoch, ok)
	}
	fence.Lose()
	if _, _, ok := fence.Token(); ok {
		t.Error("a lost lease still reported its token")
	}
	if _, _, ok := lock.NewFence(0, "e1").Token(); ok {
		t.Error("a zero token was reported as one")
	}
	if _, _, ok := lock.NewFence(42, "").Token(); ok {
		t.Error("a token with no epoch was reported as one")
	}
	var none *lock.Fence
	none.Lose()
	if _, _, ok := none.Token(); ok {
		t.Error("a nil Fence reported a token")
	}
}

// TestFencingEpoch_TwoInProcessManagersNeverShareOne: each manager's counter
// starts again, so two managers' tokens must never compare.
func TestFencingEpoch_TwoInProcessManagersNeverShareOne(t *testing.T) {
	ctx := context.Background()
	a, err := lock.NewInProcessManager().Acquire(ctx, "item", time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := lock.NewInProcessManager().Acquire(ctx, "item", time.Second, lock.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, ea, _ := lock.FencingToken(a)
	_, eb, _ := lock.FencingToken(b)
	if ea == "" || ea == eb {
		t.Errorf("two managers' epochs %q and %q; want two different, non-empty", ea, eb)
	}
}
