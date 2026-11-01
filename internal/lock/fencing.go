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
// compared with another for the same item.
package lock

import "context"

// Fenced is a Lease that knows the fencing token of its acquisition. The
// token is fixed when the lease is acquired: a KeepAlive renews the lease and
// never changes it.
type Fenced interface {
	// FencingToken is the acquisition's token, never zero.
	FencingToken() uint64
}

// FencingToken returns lease's fencing token, and false when the lease
// carries none (a test fake, or a wrapper that does not pass one on).
func FencingToken(lease Lease) (uint64, bool) {
	f, ok := lease.(Fenced)
	if !ok {
		return 0, false
	}
	token := f.FencingToken()
	return token, token != 0
}

// fencingKey marks a context carrying the fencing token of the lease its work
// runs under.
type fencingKey struct{}

// WithFencingToken returns ctx carrying token, the fencing token of the device
// lease the work under ctx holds, for whatever records that work (a Runner's
// journal publisher) to stamp on each record. It stays in this process: a
// child process is handed what it needs on its request, never a context.
func WithFencingToken(ctx context.Context, token uint64) context.Context {
	return context.WithValue(ctx, fencingKey{}, token)
}

// FencingTokenFrom returns the token WithFencingToken put on ctx, and false
// when there is none.
func FencingTokenFrom(ctx context.Context) (uint64, bool) {
	token, ok := ctx.Value(fencingKey{}).(uint64)
	return token, ok && token != 0
}
