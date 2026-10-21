// Package lock_test: a contended Acquire names who holds the item, for
// every adapter alike (Phase 103a).
package lock_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// conformanceContentionNamesOwners: a contended Acquire returns a
// *HeldError naming the holders' owners, still after a KeepAlive rewrote
// the record; an owner-less hold matches no owner; and an owner outside the
// allowed alphabet is refused before anything is written.
func conformanceContentionNamesOwners(t *testing.T, mgr lock.Manager) {
	ctx := context.Background()
	const ttl = 5 * time.Second
	held := func(t *testing.T, item string, opts lock.AcquireOptions) *lock.HeldError {
		t.Helper()
		_, err := mgr.Acquire(ctx, item, ttl, opts)
		var he *lock.HeldError
		if !errors.As(err, &he) || !errors.Is(err, lock.ErrLockHeld) {
			t.Fatalf("a contended Acquire of %s returned %v, want a *HeldError that is ErrLockHeld", item, err)
		}
		return he
	}

	exclusive := "owner-exclusive-" + t.Name()
	first, err := mgr.Acquire(ctx, exclusive, ttl, lock.AcquireOptions{Owner: "job-a_rtr1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Release(ctx) }()
	he := held(t, exclusive, lock.AcquireOptions{Owner: "job-b_rtr1"})
	if !slices.Equal(he.Owners, []string{"job-a_rtr1"}) || !he.HeldBy("job-a_rtr1") || he.HeldBy("job-b_rtr1") {
		t.Errorf("held by %v, want only job-a_rtr1", he.Owners)
	}
	if !strings.Contains(he.Error(), "job-a_rtr1") {
		t.Errorf("the error does not name its holder: %v", he)
	}
	if err := first.KeepAlive(ctx); err != nil {
		t.Fatal(err)
	}
	if he := held(t, exclusive, lock.AcquireOptions{}); !he.HeldBy("job-a_rtr1") {
		t.Errorf("after a KeepAlive the hold names %v, want job-a_rtr1 still", he.Owners)
	}

	shared := "owner-shared-" + t.Name()
	for _, owner := range []string{"reader-1", "reader-2"} {
		lease, err := mgr.Acquire(ctx, shared, ttl, lock.AcquireOptions{Mode: lock.ModeShared, Owner: owner})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lease.Release(ctx) }()
	}
	he = held(t, shared, lock.AcquireOptions{Owner: "writer"})
	if got := slices.Sorted(slices.Values(he.Owners)); !slices.Equal(got, []string{"reader-1", "reader-2"}) {
		t.Errorf("a shared hold names %v, want both readers", he.Owners)
	}

	anonymous := "owner-none-" + t.Name()
	nobody, err := mgr.Acquire(ctx, anonymous, ttl, lock.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = nobody.Release(ctx) }()
	if he := held(t, anonymous, lock.AcquireOptions{Owner: "job-c_rtr1"}); !slices.Equal(he.Owners, []string{""}) || he.HeldBy("") || he.HeldBy("job-c_rtr1") {
		t.Errorf("an owner-less hold names %v and matches an owner", he.Owners)
	}

	for _, bad := range []string{"a.b", "has space", "x>", strings.Repeat("a", 257)} {
		if _, err := mgr.Acquire(ctx, "owner-bad-"+t.Name(), ttl, lock.AcquireOptions{Owner: bad}); err == nil || errors.Is(err, lock.ErrLockHeld) {
			t.Errorf("owner %q was not refused: %v", bad, err)
		}
	}
}
