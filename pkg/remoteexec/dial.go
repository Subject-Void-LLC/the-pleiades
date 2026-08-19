package remoteexec

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

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
	dialCtx := ctx
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}

	dialer := net.Dialer{Timeout: config.Timeout}
	conn, err := dialer.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	// ssh.NewClientConn below takes no context, so cancellation during
	// the handshake (from the caller's ctx or from the config.Timeout
	// fallback above) is enforced by closing conn out from under it on a
	// separate goroutine. That unblocks the handshake with an I/O error
	// instead of letting it hang past the effective deadline. The
	// goroutine exits as soon as either dialCtx is done or the handshake
	// finishes, since done is closed via defer either way.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-dialCtx.Done():
			// A Close error here is not actionable: this goroutine exists
			// only to unblock the handshake below, and NewClientConn is
			// what surfaces the resulting I/O error to the caller.
			_ = conn.Close() // #nosec G104 -- intentional, see comment above
		case <-done:
		}
	}()

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh handshake: %w", err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// dialWithRetry attempts to dial addr up to r.opts.MaxRetries times,
// sleeping a jittered exponential backoff (pkg/retry.Backoff) between
// attempts.
//
// It wraps the dial phase ONLY: r.dial either succeeds with a fully
// handshaken client or fails outright, and nothing here ever re-attempts
// a command that has already been sent over an established session. See
// this package's own doc comment for why that boundary is load-bearing.
//
// Every failed attempt is recorded against the circuit breaker
// immediately, not just the final one, and Allow is re-checked before
// every attempt. A circuit that opens partway through this loop
// (possible whenever MaxRetries is at least BreakerThreshold) stops
// dialing at once instead of exhausting the remaining attempts.
func (r *Runner) dialWithRetry(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	var lastErr error
	for attempt := 0; attempt < r.opts.MaxRetries; attempt++ {
		if !r.breaker.Allow(addr) {
			return nil, fmt.Errorf("circuit open for %s, too many recent failures", addr)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		client, err := r.dial(ctx, addr, config)
		if err == nil {
			return client, nil
		}
		lastErr = err
		r.breaker.RecordFailure(addr)

		// Do not sleep after the final attempt; there is nothing left to
		// wait for.
		if attempt == r.opts.MaxRetries-1 {
			break
		}

		wait := retry.Backoff(defaultBackoffBase, defaultBackoffMax, attempt)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%w (last dial error: %v)", ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("dial failed after %d attempt(s): %w", r.opts.MaxRetries, lastErr)
}
