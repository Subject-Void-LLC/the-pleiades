// Package telnetexec runs a command over a bare interactive Telnet
// session (RFC 854) and is the single place this platform does that.
//
// It exists for the reason pkg/remoteexec, pkg/serialexec, and
// pkg/serialtcp exist. A Collection may import pkg/ and the standard
// library and nothing else in this module, which internal/archtest
// enforces, so a Collection method reaching a Telnet-only device cannot
// use an adapter living under internal/ no matter how much of the same
// work it needs.
//
// # Why this platform speaks Telnet at all
//
// Telnet sends everything, credentials included, in cleartext with no
// encryption at any layer. This package cannot fix that and does not try
// to; the binding that reaches it (engine.TransportBinding.RequireOptInParam)
// is what refuses to dispatch without an explicit, named opt-in, the same
// shape pkg/serialtcp's own raw-passthrough binding already established.
// It exists at all because genuinely ancient gear has no SSH: Ansible
// ships ansible.netcommon.telnet for exactly this reason, and its own
// documentation states the reason plainly, "mostly to be used for
// enabling ssh on devices that only have telnet enabled by default."
// Refusing that bootstrap case is a gap with no upside.
//
// # This client refuses every option it is offered
//
// A real Telnet client typically negotiates ECHO, SUPPRESS-GO-AHEAD, and
// terminal type. This one negotiates nothing: it replies WONT to every
// DO and DONT to every WILL a server offers, and drops any subnegotiation
// payload it receives without inspecting it. There is nothing an
// accepted option would let an Exec-shaped, one-command-in-one-command-out
// session do that plain refusal does not already cover, and refusing
// keeps the protocol surface this package must defend at its smallest —
// the same reasoning pkg/rfc2217's own doc comment gives for why THAT
// package, unlike this one, needs to actually negotiate one specific
// option (COM-PORT-OPTION) rather than refuse everything.
//
// # No exit status, ever
//
// Identical reasoning to pkg/serialexec and pkg/serialtcp's own doc
// comments: a Telnet session has no protocol-level concept of a remote
// command's exit status, so Result.ExitStatusUnknown is always true.
//
// # How "the command finished" is decided, and the one extra read this package pays for
//
// Exec reads once before writing anything, to drain and answer whatever
// opening negotiation burst the far end sends immediately on connect —
// a real telnetd commonly sends WILL ECHO / WILL SUPPRESS-GO-AHEAD before
// its login banner, and some hold that banner back until the client has
// replied. Nothing read here is discarded: it is simply the first part
// of the same Stdout a command over any of this platform's other
// byte-stream transports would produce. This is the one structural
// difference from pkg/serialtcp's otherwise identical write-then-read
// shape, and it costs one full Options.ReadTimeout of latency on every
// call even against a server with no banner at all (there is no way to
// tell "nothing is coming" from "something is coming slowly" without
// waiting out the timeout) — a documented, bounded cost, not an oversight.
//
// After that, Exec writes the command, then reads with a bounded
// per-call timeout (Options.ReadTimeout, via net.Conn.SetReadDeadline)
// in a loop, accumulating bytes, until one read call either times out
// (net.Error.Timeout() == true) or returns io.EOF, treated identically —
// pkg/serialtcp's own doc comment gives the full reasoning for why an
// EOF is exactly as valid an answer as a quiet period, not a failure.
//
// Context cancellation is checked only BETWEEN read calls, not during
// one, for the identical reason and with the identical trade-off
// pkg/serialtcp's own doc comment states.
package telnetexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Telnet command bytes this package's IAC filter recognizes (RFC 854).
const (
	iacByte  = 255
	dontByte = 254
	doByte   = 253
	wontByte = 252
	willByte = 251
	sbByte   = 250
	seByte   = 240
)

// Options configures how Exec runs one command over a Telnet session.
type Options struct {
	// DialTimeout bounds the initial TCP connection attempt. Zero takes
	// DefaultDialTimeout.
	DialTimeout time.Duration

	// ReadTimeout bounds each individual read call, both while draining
	// the opening negotiation burst and while accumulating a command's
	// output; the quiet period after the last byte is what tells Exec
	// the far end has finished writing. Zero takes DefaultReadTimeout.
	ReadTimeout time.Duration

	// MaxOutputBytes bounds the output accumulated in EACH of the two
	// read phases (the opening negotiation/banner drain, and the
	// command's own output) before that phase is refused outright, so a
	// device stuck emitting output forever cannot exhaust memory. Zero
	// takes DefaultMaxOutputBytes. The combined Stdout returned by Exec
	// can therefore be up to twice this value.
	MaxOutputBytes int
}

const (
	// DefaultDialTimeout is how long Exec waits for the initial TCP
	// connection before giving up.
	DefaultDialTimeout = 10 * time.Second

	// DefaultReadTimeout is how long one read call waits for the next
	// byte before Exec decides output has gone quiet.
	DefaultReadTimeout = 500 * time.Millisecond

	// DefaultMaxOutputBytes bounds accumulated output per read phase. 1
	// MiB, the same order of magnitude pkg/sdk's own structured-input
	// caps use elsewhere in this module for document-shaped content.
	DefaultMaxOutputBytes = 1 << 20
)

// Result is what running one command over a Telnet session produced.
type Result struct {
	// Stdout is everything the far end wrote back: the opening
	// negotiation burst and any banner accompanying it, followed by the
	// echoed command and its response.
	Stdout string

	// ExitStatusUnknown is always true: see this package's own doc
	// comment for why a Telnet session can never report one.
	ExitStatusUnknown bool
}

// Exec dials host:port over Telnet, drains and answers the far end's
// opening negotiation burst, writes command terminated by "\r\n" (the
// line ending a typed command over a terminal session expects), and
// reads back whatever the far end writes until output goes quiet, ctx is
// canceled, or opts.MaxOutputBytes is reached.
//
// There is no authentication of any kind at the transport level: see
// this package's own doc comment for why Telnet's cleartext nature is a
// fact about the protocol this package cannot change, not an oversight.
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
		return Result{}, fmt.Errorf("telnetexec: dialing %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	return ExecOverConn(ctx, conn, opts, command)
}

// ExecOverConn is Exec's own negotiation-drain-then-command logic, run
// over conn directly instead of a connection this package dials itself.
// Exec is now a thin wrapper: dial, then call this.
//
// This exists for a caller who already has a live connection to the
// device by some other means Exec cannot express — most concretely,
// pkg/remoteexec.Runner.DialThroughHops, which tunnels through a bastion
// chain (transport.Target.Route) and hands back a raw net.Conn with no
// SSH handshake on the final leg, exactly the shape a Telnet-only device
// behind a bastion needs. conn is never closed by this function; the
// caller that owns it (Exec, or whoever built the tunnel) is
// responsible for closing it once this call returns.
func ExecOverConn(ctx context.Context, conn net.Conn, opts Options, command string) (Result, error) {
	readTimeout := opts.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = DefaultReadTimeout
	}
	maxOutput := opts.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}

	f := &iacFilter{}

	banner, err := readUntilQuiet(ctx, conn, f, readTimeout, maxOutput)
	if err != nil {
		return Result{}, fmt.Errorf("telnetexec: %w", err)
	}

	if err := writeData(conn, []byte(command+"\r\n")); err != nil {
		return Result{}, fmt.Errorf("telnetexec: writing command: %w", err)
	}

	rest, err := readUntilQuiet(ctx, conn, f, readTimeout, maxOutput)
	if err != nil {
		return Result{}, fmt.Errorf("telnetexec: %w", err)
	}

	output := append(banner, rest...)
	return Result{Stdout: string(output), ExitStatusUnknown: true}, nil
}

// writeData writes data to conn, escaping any literal 0xFF byte as
// IAC IAC (RFC 854): without this, a data byte that happens to equal the
// IAC command value would be misread as the start of a telnet command by
// whatever is on the other end.
func writeData(conn io.Writer, data []byte) error {
	escaped := make([]byte, 0, len(data))
	for _, b := range data {
		if b == iacByte {
			escaped = append(escaped, iacByte, iacByte)
		} else {
			escaped = append(escaped, b)
		}
	}
	_, err := conn.Write(escaped)
	return err
}

// writeIAC writes a two-byte telnet command (IAC cmd opt) to conn.
func writeIAC(conn io.Writer, cmd, opt byte) error {
	_, err := conn.Write([]byte{iacByte, cmd, opt})
	return err
}

// readUntilQuiet accumulates plain data bytes read from conn, running
// each chunk through f so any telnet negotiation is answered and
// stripped before the data reaches the caller. It returns when a read
// call times out (net.Error.Timeout() == true) or returns io.EOF — see
// this package's own doc comment for why both mean the same thing here
// — when ctx is canceled, or when the accumulated total exceeds
// maxOutput, in which case it is refused outright rather than silently
// truncated.
//
// conn.SetReadDeadline failing, and f.feed failing because writeIAC's own
// conn.Write failed while answering a negotiation offer, are both real
// defensive coverage but neither is reachable from this package's own
// test suite without fault injection this module does not fabricate —
// the identical, already-documented gap pkg/serialexec.Exec and
// pkg/serialtcp.readUntilQuiet each carry for the same reason. Left in
// place so a future net.Conn implementation's failure mode here gets a
// clear, named error instead of an opaque one.
func readUntilQuiet(ctx context.Context, conn net.Conn, f *iacFilter, readTimeout time.Duration, maxOutput int) ([]byte, error) {
	var out []byte
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}

		if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return out, fmt.Errorf("setting read deadline: %w", err)
		}

		n, err := conn.Read(buf)
		if n > 0 {
			data, ferr := f.feed(conn, buf[:n])
			if ferr != nil {
				return out, ferr
			}
			out = append(out, data...)
			if len(out) > maxOutput {
				return nil, fmt.Errorf("output exceeded %d bytes without going quiet", maxOutput)
			}
		}
		if err != nil {
			var netErr net.Error
			if (errors.As(err, &netErr) && netErr.Timeout()) || errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
	}
}

// filterState is where iacFilter is within one telnet command sequence,
// carried across separate feed calls (and therefore across separate
// net.Conn.Read calls) so a command split across two reads is still
// parsed correctly.
type filterState int

const (
	stateData filterState = iota
	stateIAC
	stateNegotiate
	stateSB
	stateSBIAC
)

// iacFilter strips telnet IAC command sequences out of a raw byte stream,
// refusing every option offered (see this package's own doc comment for
// why), and leaves plain data bytes for the caller. One iacFilter is used
// for a whole Exec call, so state carries correctly across the banner-drain
// read and the post-command read.
type iacFilter struct {
	state       filterState
	negotiating byte
}

// feed processes raw, just-read bytes, writing any required negotiation
// replies to conn immediately, and returns the plain data bytes with all
// IAC sequences and subnegotiation payloads removed.
func (f *iacFilter) feed(conn io.Writer, raw []byte) ([]byte, error) {
	var data []byte
	for _, b := range raw {
		switch f.state {
		case stateData:
			if b == iacByte {
				f.state = stateIAC
			} else {
				data = append(data, b)
			}
		case stateIAC:
			switch b {
			case iacByte:
				data = append(data, 0xFF)
				f.state = stateData
			case doByte, dontByte, willByte, wontByte:
				f.negotiating = b
				f.state = stateNegotiate
			case sbByte:
				f.state = stateSB
			default:
				// A single-byte telnet command (NOP, GA, ...): consumed,
				// no reply needed.
				f.state = stateData
			}
		case stateNegotiate:
			// b is the option byte the peer is negotiating. This client
			// refuses every option: DO/WILL get a WONT/DONT reply.
			// DONT/WONT need no reply at all (RFC 854: a reply is only
			// required to acknowledge a state change, and this client is
			// already refusing everything, so a DONT/WONT confirms what
			// it already believes) — replying anyway would risk a
			// negotiation loop with a server that follows the same rule.
			switch f.negotiating {
			case doByte:
				if err := writeIAC(conn, wontByte, b); err != nil {
					return data, err
				}
			case willByte:
				if err := writeIAC(conn, dontByte, b); err != nil {
					return data, err
				}
			}
			f.state = stateData
		case stateSB:
			if b == iacByte {
				f.state = stateSBIAC
			}
			// Else: subnegotiation payload byte. This client never
			// agrees to any option, so it should never legitimately
			// receive one; if a server sends one anyway it is dropped
			// rather than fed into the plain-data output.
		case stateSBIAC:
			switch b {
			case seByte:
				f.state = stateData
			case iacByte:
				// Escaped 0xFF inside the subnegotiation payload.
				f.state = stateSB
			default:
				// Malformed: not IAC-escape or SE after IAC inside SB.
				// Fail closed by resuming as subnegotiation payload
				// rather than reinterpreting b as a fresh command, so a
				// truncated or hostile frame cannot desynchronize this
				// state machine into misreading attacker-controlled
				// bytes as arbitrary telnet commands.
				f.state = stateSB
			}
		}
	}
	return data, nil
}
