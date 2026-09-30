package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
)

// dialFunc dials addr and returns a fully handshaken SSH client, or an
// error if the TCP connection or the SSH handshake failed.
//
// It is a named type so Runner.dial can be swapped out in this package's
// own tests, never from outside it, to exercise retry, backoff and
// circuit-breaker behavior deterministically with no real socket.
type dialFunc func(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error)

// realDial is the default dialFunc: a real TCP dial followed by the SSH
// handshake, with the ENTIRE sequence (not just the TCP connect step)
// bounded by whichever comes sooner, ctx's own deadline or
// config.Timeout as a fallback.
//
// config.Timeout still matters when ctx already carries a deadline
// (context.WithTimeout only ever shortens an effective deadline), but it
// is essential when ctx has no deadline at all: a caller passing
// context.Background() must still get a bounded dial, not one that can
// hang for however long the OS takes to notice a dead TCP connection.
// That is not hypothetical. Measured against a stopped container, the
// TCP connect is accepted or silently swallowed well past any sane
// per-call budget while the SSH handshake that follows never receives a
// single byte.
func realDial(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	dialCtx, cancel := handshakeContext(ctx, config)
	defer cancel()

	dialer := net.Dialer{Timeout: config.Timeout}
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	defer closeOnDone(dialCtx, conn)()

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// handshakeContext returns ctx bounded by config.Timeout as well, when
// one is set: the bound every SSH handshake here runs under, since a
// caller passing context.Background() must still get a bounded dial.
func handshakeContext(ctx context.Context, config *ssh.ClientConfig) (context.Context, context.CancelFunc) {
	if config.Timeout > 0 {
		return context.WithTimeout(ctx, config.Timeout)
	}
	return ctx, func() {}
}

// closeOnDone closes conn when ctx ends, until the returned stop is
// called. ssh.NewClientConn takes no context, and a connection tunneled
// through a hop supports no deadline, so closing conn out from under a
// handshake is the one way to unblock it with an I/O error instead of
// letting it hang past the effective deadline. The goroutine exits as
// soon as either ctx is done or stop is called, and stop returns only once
// it has: a caller that cancels ctx right after stop (every caller here,
// through its deferred cancel) must not find the goroutine still choosing,
// with both channels ready, and closing a connection that just succeeded.
func closeOnDone(ctx context.Context, conn net.Conn) (stop func()) {
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case <-ctx.Done():
			// A Close error here is not actionable: this goroutine exists
			// only to unblock the handshake, and NewClientConn is what
			// surfaces the resulting I/O error to the caller.
			_ = conn.Close() // #nosec G104 -- intentional, see comment above
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

// dialWithRetry attempts to dial addr, using dial, up to r.opts.MaxRetries
// times, sleeping a jittered exponential backoff (pkg/retry.Backoff)
// between attempts. The loop and the sleep are pkg/retry.Do's, shared with
// internal/lock's own retry loops rather than hand-rolled a third time
// (PLAN.md Section 1379's Build-Once table); this function supplies only
// what is specific to a dial attempt: the breaker check, the dial call
// itself, and which of a dial attempt's failures should stop the loop
// outright versus be retried.
//
// dial is an explicit parameter, not always r.dial, because a hop chain's
// first leg is dialed directly (r.dial: a real TCP connect, or this
// package's own tests' substitute) while every leg after it is reached by
// tunneling through the previous leg's already-authenticated connection
// (dialThroughHop). Both need the identical retry, backoff and
// circuit-breaker treatment, keyed by that leg's own address; only the
// underlying "how do bytes reach this address at all" mechanism differs.
//
// It wraps the dial phase ONLY: dial either succeeds with a fully
// handshaken client or fails outright, and nothing here ever re-attempts
// a command that has already been sent over an established session. See
// this package's own doc comment for why that boundary is load-bearing.
//
// Every failed attempt is recorded against the circuit breaker
// immediately, not just the final one, and Allow is re-checked before
// every attempt (each call into fn below is one attempt). A circuit that
// opens partway through this loop (possible whenever MaxRetries is at
// least BreakerThreshold) stops dialing at once instead of exhausting the
// remaining attempts: breaker.ErrOpen is the one error retryable reports
// false for, so retry.Do returns immediately rather than sleeping and
// trying again.
func (r *Runner) dialWithRetry(ctx context.Context, dial dialFunc, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	fn := func(ctx context.Context) (*ssh.Client, error) {
		// The context comes first. Allow hands out the half-open probe,
		// and only a recorded outcome gives it back, so a call that
		// claimed it and then returned for a done context kept it and
		// latched the circuit half-open for the life of the process
		// (FAILURE_PATTERNS 398). A done context has nothing to record.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !r.breaker.Allow(addr) {
			return nil, fmt.Errorf("%w for %s, too many recent failures", breaker.ErrOpen, addr)
		}

		client, err := dial(ctx, addr, config)
		if err != nil {
			r.breaker.RecordFailure(addr)
			return nil, err
		}
		return client, nil
	}

	delay := func(attempt int) time.Duration {
		return retry.Backoff(defaultBackoffBase, defaultBackoffMax, attempt)
	}
	retryable := func(err error) bool { return !errors.Is(err, breaker.ErrOpen) }

	return retry.Do(ctx, delay, r.opts.MaxRetries, retryable, fn)
}
