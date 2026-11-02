// Package lock: fencing tokens, the order of a lease's acquisition.
//
// A fencing token is a number a lock manager hands out with each acquisition
// that only grows for an item: a lease taken after another one ended holds a
// larger token. It lets a reader decide which of two holders of one device
// came later without comparing two machines' clocks, which nothing here
// bounds (a clock stepped back, or two Runners skewed). The rollback planner
// orders runs on a device by it (internal/rollback).
//
// The NATS manager's token is the JetStream sequence of the acquisition's
// write: stream-wide, so it grows across every key, across a release and a
// re-acquire, and across manager restarts, with gaps. The in-process
// manager's is its holder counter, which grows for the life of the process.
// Neither is comparable across the two managers, and a token is only ever
// compared with another for the same item and the same epoch.
//
// The epoch names the life of the sequence a token comes from: the NATS lock
// bucket's stream, by its creation time, which a restart with its storage
// keeps and a re-creation (lost data, a deleted bucket, a reinstall) changes;
// the in-process manager's own random id. Two tokens from different epochs
// say nothing about their order, since a re-created stream counts from one
// again.
package lock

import (
	"context"
	"sync/atomic"
)

// Fenced is a Lease that knows the fencing token of its acquisition. The
// token is fixed when the lease is acquired: a KeepAlive renews the lease and
// never changes it.
type Fenced interface {
	// FencingToken is the acquisition's token, never zero.
	FencingToken() uint64
	// FencingEpoch names the life of the sequence the token comes from, empty
	// when it could not be read; a token is compared only within one epoch.
	FencingEpoch() string
}

// FencingToken returns lease's fencing token and its epoch, and false when
// the lease carries none (a test fake, or a wrapper that does not pass one
// on) or its epoch is unknown, since such a token cannot be compared.
func FencingToken(lease Lease) (uint64, string, bool) {
	f, ok := lease.(Fenced)
	if !ok {
		return 0, "", false
	}
	token, epoch := f.FencingToken(), f.FencingEpoch()
	return token, epoch, token != 0 && epoch != ""
}

// Fence is the fencing token of the lease some work runs under, for whatever
// records that work to stamp on each record, until the lease is lost. A
// holder that keeps working after its lease lapsed (a renewal failed, and the
// work could not be stopped) is no longer the device's only holder: another
// may have taken the device with a larger token, so records written then
// carry none and are ordered some other way.
type Fence struct {
	token uint64
	epoch string
	lost  atomic.Bool
}

// NewFence returns a Fence for a lease's token and its epoch.
func NewFence(token uint64, epoch string) *Fence {
	return &Fence{token: token, epoch: epoch}
}

// Token returns the fencing token and its epoch, and false when there is
// none: a nil Fence, one whose lease was lost, or one with no epoch.
func (f *Fence) Token() (uint64, string, bool) {
	if f == nil || f.lost.Load() || f.token == 0 || f.epoch == "" {
		return 0, "", false
	}
	return f.token, f.epoch, true
}

// Lose records that the lease was lost. It stays lost: a later renewal that
// succeeds does not prove nobody else held the device in between.
func (f *Fence) Lose() {
	if f != nil {
		f.lost.Store(true)
	}
}

// fencingKey marks a context carrying the Fence of the lease its work runs
// under.
type fencingKey struct{}

// WithFence returns ctx carrying fence, for whatever records the work under
// ctx (a Runner's journal publisher) to read on each record. It stays in this
// process: a child process is handed what it needs on its request, never a
// context.
func WithFence(ctx context.Context, fence *Fence) context.Context {
	return context.WithValue(ctx, fencingKey{}, fence)
}

// FenceFrom returns the Fence WithFence put on ctx, nil when there is none.
func FenceFrom(ctx context.Context) *Fence {
	fence, _ := ctx.Value(fencingKey{}).(*Fence)
	return fence
}
