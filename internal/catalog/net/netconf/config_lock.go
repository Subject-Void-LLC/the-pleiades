// Package netconf: the datastore lock a task holds, released only when
// nothing the task staged can still sit in the candidate datastore.
//
// RFC 6241 does not say an unlock discards the candidate, and an unlock that
// left an edit staged would hand it to the next commit by any client. What is
// measured (pkg/netconf's TestAssurance_* on Netopeer2) is that a session
// killed while it holds the lock leaves the candidate as running. So a task
// whose discard failed, or was not proven, keeps the lock and ends its session
// by closing the stream, never by an unlock.
package netconf

import "context"

// heldLock is the lock one task took on its session's datastore.
type heldLock struct {
	// ctx is the task's context, which cleanup outlives.
	ctx     context.Context
	session netconfSession
	// held says the lock was taken.
	held bool
	// staged says an edit of this task may still be in the candidate: the
	// lock is then not released, and the session's close ends it.
	staged bool
}

// take takes the lock, reporting the device's refusal.
func (l *heldLock) take(ctx context.Context) error {
	if err := l.session.Lock(ctx); err != nil {
		return err
	}
	l.held = true
	return nil
}

// release unlocks, on a context of its own, unless the lock was not taken or
// an edit of this task may still be staged.
func (l *heldLock) release() {
	if !l.held || l.staged {
		return
	}
	cctx, cancel := cleanupContext(l.ctx)
	defer cancel()
	_ = l.session.Unlock(cctx)
}

// discard throws away what this task staged in the candidate, on a context of
// its own (a task canceled or timed out mid-edit still discards), and keeps
// the lock held when the discard fails, so the edit cannot outlive the
// session.
func (l *heldLock) discard() error {
	cctx, cancel := cleanupContext(l.ctx)
	defer cancel()
	if err := l.session.DiscardChanges(cctx); err != nil {
		l.staged = true
		return err
	}
	l.staged = false
	return nil
}
