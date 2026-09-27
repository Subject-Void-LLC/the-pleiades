package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/retry"
)

// DialThroughHops dials through hops, in order, completing an
// independent SSH handshake at each one -- identical machinery to
// Connect: circuit breaker, retry, backoff, and each hop's own host key
// verification (dialChain, shared with Connect) -- then opens a raw
// byte-stream channel from the last hop (or, if hops is empty, a direct
// TCP dial) to target, with NO SSH handshake performed on that final
// leg.
//
// This is Connect's non-SSH counterpart. Connect treats every leg,
// target included, as its own independent SSH connection, which is
// exactly right for ssh_exec but cannot serve a caller speaking a
// different protocol over the final leg - a console server's raw TCP
// passthrough, or RFC 2217, neither of which is SSH. DialThroughHops
// hands such a caller a raw net.Conn instead: everything past the last
// hop's own authenticated connection is this method's caller's problem,
// not this package's, exactly the same division of responsibility
// dialThroughHop's own doc comment describes for ssh_exec's tunneled
// leg, just stopped one step earlier.
//
// The final leg (the direct-tcpip channel open, or the direct TCP dial
// when hops is empty) gets the identical circuit-breaker and retry
// treatment every other leg gets, keyed by target's own address, via
// dialFinalLegWithRetry - the net.Conn-returning analogue of
// dialWithRetry, since a channel open can fail for the same
// unreachable-target reasons a real dial can and deserves the same
// resilience, not a bare best-effort attempt.
//
// The returned net.Conn's own Close method closes the tunneled channel
// AND every hop's own SSH client, innermost first (the same order
// Connect's own cleanup already uses), so a caller needs only the one
// Close call to release the whole chain. A caller must never close a
// hop's own client directly: that would sever every channel tunneled
// through it, including this one.
func (r *Runner) DialThroughHops(ctx context.Context, hops []Hop, target Target) (conn net.Conn, err error) {
	legs := make([]connectLeg, len(hops))
	for i, h := range hops {
		legs[i] = connectLeg{addr: h.Target.Addr(), auth: h.Auth, insecureSkipHostKeyVerify: h.InsecureSkipHostKeyVerify}
	}

	chain, err := r.dialChain(ctx, legs)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			for i := len(chain) - 1; i >= 0; i-- {
				_ = chain[i].Close()
			}
		}
	}()

	var finalDial finalDialFunc
	if len(chain) == 0 {
		finalDial = func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			c, dialErr := d.DialContext(ctx, "tcp", addr)
			if dialErr != nil {
				return nil, fmt.Errorf("tcp dial: %w", dialErr)
			}
			return c, nil
		}
	} else {
		lastHop := chain[len(chain)-1]
		finalDial = func(ctx context.Context, addr string) (net.Conn, error) {
			c, dialErr := lastHop.DialContext(ctx, "tcp", addr)
			if dialErr != nil {
				return nil, fmt.Errorf("open tunneled channel to %s: %w", addr, dialErr)
			}
			return c, nil
		}
	}

	finalConn, dialErr := r.dialFinalLegWithRetry(ctx, finalDial, target.Addr())
	if dialErr != nil {
		return nil, fmt.Errorf("remoteexec: dial %s: %w", target.Addr(), dialErr)
	}
	r.breaker.RecordSuccess(target.Addr())

	return newHopTunneledConn(finalConn, chain), nil
}

// hopTunneledConn wraps the final raw net.Conn DialThroughHops opens.
//
// Its embedded net.Conn is NOT the tunneled channel itself: when hops is
// non-empty, that channel is golang.org/x/crypto/ssh's own tcpChan type,
// which refuses every deadline call outright ("ssh: tcpChan: deadline
// not supported" - confirmed empirically against a real tunnel while
// writing this package's own tests, not assumed from documentation).
// Every real consumer this method exists for (pkg/serialtcp,
// pkg/rfc2217, pkg/telnetexec) depends on SetReadDeadline actually
// working for its own "read until quiet"/timeout logic, so handing one
// a bare tunneled channel would break it on the first read. Instead,
// newHopTunneledConn hands the caller one half of a real net.Pipe()
// (which DOES support real deadlines, Go's own documented behavior) and
// runs two background goroutines that pump bytes between the other half
// and the real tunneled channel - the standard shape for retrofitting
// deadline support onto a conn type that lacks it.
//
// This wrapper is used unconditionally, even for a zero-hop
// DialThroughHops call whose "tunneled channel" is actually a plain
// *net.TCPConn that already supports deadlines fine: a caller gets the
// identical type and closing behavior regardless of how many hops were
// involved, rather than two subtly different net.Conn implementations
// depending on Route length.
type hopTunneledConn struct {
	net.Conn // the caller-facing half of a net.Pipe(): real deadline support
	real     net.Conn
	chain    []*ssh.Client
	pumpDone chan struct{}
}

func newHopTunneledConn(real net.Conn, chain []*ssh.Client) *hopTunneledConn {
	callerSide, pumpSide := net.Pipe()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = io.Copy(pumpSide, real) }()
		go func() { defer wg.Done(); _, _ = io.Copy(real, pumpSide) }()
		wg.Wait()
	}()
	return &hopTunneledConn{Conn: callerSide, real: real, chain: chain, pumpDone: pumpDone}
}

// Close closes both the caller-facing pipe half and the real tunneled
// connection - closing only one would leave the pump goroutine copying
// the OTHER direction permanently blocked on whichever call it never
// closed, since the two pumps block on independent connections and
// closing one does not unblock a read pending on the other - waits for
// both pump goroutines to actually exit (so Close is a real
// synchronization point, not a fire-and-forget that could still be
// running when this returns), then closes every hop's own SSH client,
// innermost first. The first error encountered is returned; every step
// is still attempted regardless, so one failure never leaves a later
// resource leaked.
func (c *hopTunneledConn) Close() error {
	err := c.Conn.Close()
	if rerr := c.real.Close(); err == nil {
		err = rerr
	}
	<-c.pumpDone

	for i := len(c.chain) - 1; i >= 0; i-- {
		if cerr := c.chain[i].Close(); err == nil {
			err = cerr
		}
	}
	return err
}

// finalDialFunc dials addr and returns a raw net.Conn, performing NO SSH
// handshake - DialThroughHops' own analogue of dialFunc (dial.go), which
// always includes one.
type finalDialFunc func(ctx context.Context, addr string) (net.Conn, error)

// dialFinalLegWithRetry mirrors dialWithRetry exactly (the same breaker
// check, the same jittered exponential backoff, the same errCircuitOpen
// short-circuit that stops the loop immediately instead of exhausting
// retries against an open circuit), for DialThroughHops' own final leg,
// which returns a net.Conn rather than a *ssh.Client and so cannot reuse
// dialWithRetry's own concrete-typed signature directly.
func (r *Runner) dialFinalLegWithRetry(ctx context.Context, dial finalDialFunc, addr string) (net.Conn, error) {
	fn := func(ctx context.Context) (net.Conn, error) {
		if !r.breaker.Allow(addr) {
			return nil, fmt.Errorf("%w for %s, too many recent failures", errCircuitOpen, addr)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		conn, err := dial(ctx, addr)
		if err != nil {
			r.breaker.RecordFailure(addr)
			return nil, err
		}
		return conn, nil
	}

	delay := func(attempt int) time.Duration {
		return retry.Backoff(defaultBackoffBase, defaultBackoffMax, attempt)
	}
	retryable := func(err error) bool { return !errors.Is(err, errCircuitOpen) }

	return retry.Do(ctx, delay, r.opts.MaxRetries, retryable, fn)
}
