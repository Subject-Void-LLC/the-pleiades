package ssh

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/retry"
)

// realDial is the default dialFunc (ssh.go): a real TCP dial followed by
// the SSH handshake, with the ENTIRE sequence (not just the TCP connect
// step) bounded by whichever is sooner: ctx's own deadline, or
// config.Timeout used as a fallback. It is what New wires
// sshTransport.dial to; only this package's own test files ever
// substitute a different dialFunc.
//
// config.Timeout matters here even when ctx already carries a deadline
// of its own (context.WithTimeout only ever shortens an effective
// deadline, never lengthens one, so an earlier ctx deadline still wins),
// but it is essential when ctx has NO deadline at all: a caller that
// passes context.Background() must still get a bounded dial, not one
// that can hang for however long the OS takes to notice a dead TCP
// connection (observed in practice against a stopped target: a
// TCP-level connect can be accepted, or silently swallowed, well past
// any sane per-call budget, while the SSH handshake that follows never
// receives a single byte).
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

	// ssh.NewClientConn below does not itself accept a context, so
	// dialCtx's cancellation (whether from the caller's own ctx or from
	// the config.Timeout fallback above) during the handshake is
	// enforced by closing conn out from under it on a separate
	// goroutine. That unblocks the handshake with an I/O error instead
	// of letting it hang past the effective deadline; the goroutine
	// exits as soon as either dialCtx is done or the handshake finishes
	// (done is closed via defer either way).
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-dialCtx.Done():
			// A Close error here is not actionable: this goroutine's only
			// purpose is unblocking the handshake below, and NewClientConn
			// is what surfaces the resulting I/O error to the caller.
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

// dialWithRetry attempts to dial addr up to t.opts.MaxRetries times,
// sleeping a jittered exponential backoff (retry.Backoff) between
// attempts. It ONLY wraps the dial phase: t.dial either succeeds with a
// fully handshaken *ssh.Client or fails outright, and nothing here ever
// re-attempts a command that has already been sent over an established
// session. See Exec's doc comment (ssh.go) for the full rationale on why
// that boundary matters.
//
// Every failed attempt is recorded against the circuit breaker
// immediately (not just the final one), and Allow is re-checked before
// every attempt, so a circuit that opens partway through this loop
// (possible whenever MaxRetries >= BreakerThreshold) stops dialing
// immediately instead of exhausting the remaining attempts.
func (t *sshTransport) dialWithRetry(ctx context.Context, addr string, config *ssh.ClientConfig) (*ssh.Client, error) {
	var lastErr error
	for attempt := 0; attempt < t.opts.MaxRetries; attempt++ {
		if !t.breaker.Allow(addr) {
			return nil, fmt.Errorf("circuit open for %s, too many recent failures", addr)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		client, err := t.dial(ctx, addr, config)
		if err == nil {
			return client, nil
		}
		lastErr = err
		t.breaker.RecordFailure(addr)

		// Do not sleep after the final attempt; there is nothing left to
		// wait for.
		if attempt == t.opts.MaxRetries-1 {
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
	return nil, fmt.Errorf("dial failed after %d attempt(s): %w", t.opts.MaxRetries, lastErr)
}
