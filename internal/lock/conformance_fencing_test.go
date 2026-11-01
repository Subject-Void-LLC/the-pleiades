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
	token := func(t *testing.T, lease lock.Lease) uint64 {
		t.Helper()
		got, ok := lock.FencingToken(lease)
		if !ok {
			t.Fatalf("a %T carries no fencing token", lease)
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

// TestFencingTokenFromContext: the context helpers carry a token and report
// none when there is none.
func TestFencingTokenFromContext(t *testing.T) {
	if _, ok := lock.FencingTokenFrom(context.Background()); ok {
		t.Error("a bare context reported a fencing token")
	}
	if got, ok := lock.FencingTokenFrom(lock.WithFencingToken(context.Background(), 42)); !ok || got != 42 {
		t.Errorf("FencingTokenFrom = %d, %v; want 42, true", got, ok)
	}
	if _, ok := lock.FencingTokenFrom(lock.WithFencingToken(context.Background(), 0)); ok {
		t.Error("a zero token was reported as one")
	}
}
