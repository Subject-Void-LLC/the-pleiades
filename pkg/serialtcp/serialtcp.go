// Package serialtcp runs a command over a raw TCP passthrough
// connection to a console or terminal server (Digi, Opengear,
// Lantronix, Perle, Avocent/Cyclades) and is the single place this
// platform does that.
//
// It exists for the reason pkg/remoteexec and pkg/serialexec exist. A
// Collection may import pkg/ and the standard library and nothing else
// in this module, which internal/archtest enforces, so a Collection
// method reaching a console server cannot use an adapter living under
// internal/ no matter how much of the same work it needs.
//
// # This is a bare byte pipe, and this package cannot make it anything else
//
// Raw TCP passthrough moves bytes with ZERO framing, ZERO
// authentication, and ZERO encryption at the protocol level: any host
// that can reach the console server's TCP port can read or write
// whatever the attached serial line carries. This package cannot fix
// that and must not appear to -- it is exactly why the transport
// binding that reaches this package requires a named, loud opt-in
// rather than being reachable by default (see
// internal/transport/serialtcp). There is also no line control at all:
// no baud rate, no DTR/RTS, no break signal. RFC2217Capable
// (pkg/capability) is the sibling capability for a console server that
// offers real control; this package never negotiates any of it.
//
// # No exit status, ever, and how "the command finished" is decided
//
// Identical reasoning to pkg/serialexec's own doc comment: a raw byte
// pipe has no protocol-level concept of a remote command's exit status,
// so Result.ExitStatusUnknown is always true. Exec writes the command,
// then reads with a bounded per-call deadline (Options.ReadTimeout,
// via net.Conn.SetReadDeadline) in a loop, accumulating bytes, until one
// read call either times out -- net.Conn's own documented signal
// (net.Error.Timeout() == true) that no more output is coming for now
// -- or returns io.EOF, treated identically: a raw byte pipe has no
// session semantics to say whether the far end closing the connection
// was deliberate or not, and "here is what came back before it
// stopped" is exactly as valid an answer either way, not a failure.
// Unlike a local serial line's termios-based "(0, nil) means timeout"
// contract, a TCP net.Conn signals a deadline expiring with a genuine
// error that must be distinguished from a real read failure by its
// Timeout() method, not by its byte count.
//
// Context cancellation is checked only BETWEEN read calls, not during
// one: a blocking net.Conn.Read already committed to its own deadline
// cannot be preempted mid-call without closing the connection from
// another goroutine, which would race with a concurrent read. This
// bounds cancellation latency by whichever is shorter, the remaining
// context deadline or Options.ReadTimeout, a documented trade-off
// rather than a silent gap -- the identical trade-off
// pkg/serialexec.Exec makes for the same reason.
package serialtcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Options configures how Exec runs one command over a raw TCP
// passthrough connection.
type Options struct {
	// DialTimeout bounds the initial TCP connection attempt. Zero takes
	// DefaultDialTimeout.
	DialTimeout time.Duration

	// ReadTimeout bounds each individual read call while accumulating a
	// command's output; the quiet period after the last byte is what
	// tells Exec the far end has finished writing. Zero takes
	// DefaultReadTimeout.
	ReadTimeout time.Duration

	// MaxOutputBytes bounds the total output accumulated before a run is
	// refused outright, so a device stuck emitting output forever cannot
	// exhaust memory. Zero takes DefaultMaxOutputBytes.
	MaxOutputBytes int
}

const (
	// DefaultDialTimeout is how long Exec waits for the initial TCP
	// connection before giving up.
	DefaultDialTimeout = 10 * time.Second

	// DefaultReadTimeout is how long one read call waits for the next
	// byte before Exec decides output has gone quiet.
	DefaultReadTimeout = 500 * time.Millisecond

	// DefaultMaxOutputBytes bounds accumulated output. 1 MiB, the same
	// order of magnitude pkg/sdk's own structured-input caps use
	// elsewhere in this module for document-shaped content.
	DefaultMaxOutputBytes = 1 << 20
)

// Result is what running one command over a raw TCP passthrough
// connection produced.
type Result struct {
	// Stdout is everything the far end wrote back before output went
	// quiet.
	Stdout string

	// ExitStatusUnknown is always true: see this package's own doc
	// comment for why a raw byte pipe can never report one.
	ExitStatusUnknown bool
}

// Exec dials host:port over plain TCP, writes command terminated by
// "\r\n" (the line ending a typed command over a terminal session
// expects), and reads back whatever the far end writes until output
// goes quiet, ctx is canceled, or opts.MaxOutputBytes is reached.
//
// There is no authentication of any kind, at any layer: see this
// package's own doc comment for why that is a fact about the protocol
// this package cannot change, not an oversight.
func Exec(ctx context.Context, host string, port int, opts Options, command string) (Result, error) {
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	var d net.Dialer
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return Result{}, fmt.Errorf("serialtcp: dialing %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	return ExecOverConn(ctx, conn, opts, command)
}

// ExecOverConn is Exec's own write-then-read-until-quiet logic, run over
// conn directly instead of a connection this package dials itself. Exec
// is now a thin wrapper: dial, then call this.
//
// This exists for a caller who already has a live connection to the
// console server by some other means Exec cannot express — most
// concretely, pkg/remoteexec.Runner.DialThroughHops, which tunnels
// through a bastion chain (transport.Target.Route) and hands back a raw
// net.Conn with no SSH handshake on the final leg, exactly the shape a
// console server behind a bastion needs. conn is never closed by this
// function; the caller that owns it (Exec, or whoever built the tunnel)
// is responsible for closing it once this call returns.
func ExecOverConn(ctx context.Context, conn net.Conn, opts Options, command string) (Result, error) {
	// A Write immediately after a successful dial failing without a
	// forced fault (closing the connection from elsewhere, which would
	// race this call) is not something this package's test suite
	// fabricates, the same class of gap the SetReadDeadline check below
	// documents for itself.
	if _, err := conn.Write([]byte(command + "\r\n")); err != nil {
		return Result{}, fmt.Errorf("serialtcp: writing command: %w", err)
	}

	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultReadTimeout
	}
	maxOutput := opts.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}

	output, err := readUntilQuiet(ctx, conn, readTimeout, maxOutput)
	if err != nil {
		return Result{}, fmt.Errorf("serialtcp: %w", err)
	}

	return Result{Stdout: string(output), ExitStatusUnknown: true}, nil
}

// readUntilQuiet accumulates bytes from conn until a read call times out
// (net.Error.Timeout() == true: see this package's own doc comment),
// ctx is canceled, or the accumulated total exceeds maxOutput, in which
// case it is refused outright rather than silently truncated -- the
// same posture this module's other document-shaped-input caps take.
func readUntilQuiet(ctx context.Context, conn net.Conn, readTimeout time.Duration, maxOutput int) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}

		// SetReadDeadline on a connection this function itself just
		// finished reading from successfully essentially never fails
		// without the connection already being unusable in some other,
		// louder way; this branch has no test exercising it for the
		// same reason pkg/serialexec.Exec's SetReadTimeout/Write checks
		// do not, and is kept for the same reason: a future net.Conn
		// implementation's failure mode here deserves a clear, named
		// error rather than a generic one from deeper in the standard
		// library.
		if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return out, fmt.Errorf("setting read deadline: %w", err)
		}

		n, err := conn.Read(buf)
		if err != nil {
			// Both a read timeout and the far end closing the
			// connection mean the same thing here: no more output is
			// coming. A raw byte pipe has no session semantics to say
			// which one happened on purpose, and this package's whole
			// premise (transport.Result.ExitStatusUnknown) is that it
			// cannot know more than "here is what came back before it
			// stopped" -- an EOF is exactly as valid an answer to that
			// as a quiet period is, not a failure.
			var netErr net.Error
			if (errors.As(err, &netErr) && netErr.Timeout()) || errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}

		out = append(out, buf[:n]...)
		if len(out) > maxOutput {
			return nil, fmt.Errorf("output exceeded %d bytes without going quiet", maxOutput)
		}
	}
}
