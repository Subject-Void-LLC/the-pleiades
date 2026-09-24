package remoteexec

import (
	"context"

	"golang.org/x/crypto/ssh"
)

// maxSubsystemStderrBytes is the stderr bound a Subsystem applies. It is
// the shared stream bound under its original name, kept so the bound a
// NETCONF session was proven against keeps being named for what it
// bounds; see maxStreamStderrBytes for the reasoning behind its size.
const maxSubsystemStderrBytes = maxStreamStderrBytes

// Subsystem is one live SSH subsystem channel on a Conn: the third
// session shape this package opens, after Run/RunWithStdin's one-shot
// exec request and Shell's interactive PTY (Start's Process is the
// fourth). RFC 4254 section 6.5 makes a subsystem a named, structured
// service on the same "session" channel type the other shapes already
// use, so this costs a new SSH channel and nothing else, which is the
// whole reason it lives on Conn rather than behind a second dialer.
//
// It is an io.ReadWriteCloser and deliberately nothing more. Everything
// above it (framing, a hello exchange, request/response correlation)
// belongs to the protocol package driving it, the same division
// RunWithStdin draws between "ship bytes over SSH" and "interpret what
// came back," and the same one Shell draws against pkg/netcli. Being a
// plain io.ReadWriteCloser is also what lets a protocol package be
// tested over an in-memory pipe and run over a real SSH channel through
// one identical code path, rather than growing a mock of the transport
// it is supposed to be proving.
//
// # Standard error is a separate stream here, unlike Shell
//
// Shell requests a PTY, and a PTY merges standard error into standard
// output at the terminal layer, so it has one stream and no stderr
// accessor. A subsystem request makes no PTY, so the two stay separate,
// and RFC 6242 section 3 explicitly permits a NETCONF server to write
// diagnostics to standard error. Those bytes are drained continuously
// into a bounded buffer and reported by Stderr and in Read's own error
// text: a server that rejects what it was sent and explains why on
// standard error would otherwise reach the caller as an unexplained
// end-of-file.
//
// A Subsystem is not safe for concurrent use by multiple goroutines,
// the same restriction Conn and Shell carry, with one deliberate
// exception: the stderr drain runs in its own goroutine, so Stderr may
// be called while a Read is in flight.
//
// # The context governs the whole session, not one call
//
// io.Reader and io.Writer take no context, so the ctx passed to
// Subsystem governs the session's entire lifetime rather than a single
// call: when it is done, the underlying session is closed and every
// subsequent Read and Write fails reporting ctx's own error. This is a
// real difference from Shell, whose methods each take their own ctx,
// and it is the shape a long-lived protocol session actually wants: a
// NETCONF session lives for a task, the task has one deadline, and a
// half-open session that looks healthy while its deadline has passed is
// worse than a closed one.
type Subsystem struct {
	// s is the shared stream implementation (stream.go), which Process
	// uses too, so the two cannot drift in how they bound standard error
	// or honor a deadline.
	s *stream
}

// Subsystem opens the named SSH subsystem on this connection (RFC 4254
// section 6.5): "netconf" for a NETCONF session, "sftp" for SFTP, and so
// on. The caller must Close the result.
//
// It shares c's already-authenticated *ssh.Client, so this costs a new
// SSH channel rather than a new TCP connection, key exchange and
// authentication round. That is not merely an optimization: it is what
// keeps this package's fail-closed host key verification (knownhosts.go),
// its shared pkg/retry.Do dial loop and its per-target circuit breaker
// covering a subsystem session too, instead of a protocol package
// growing a second SSH dialer that would have to reimplement all three
// and could quietly get any of them wrong.
//
// ctx governs the whole session, not just this call; see Subsystem's own
// doc comment for why, and for what a caller sees after it is done.
func (c *Conn) Subsystem(ctx context.Context, name string) (*Subsystem, error) {
	s, err := c.openStream(ctx, name+" subsystem", func(session *ssh.Session) error {
		return session.RequestSubsystem(name)
	})
	if err != nil {
		return nil, err
	}
	return &Subsystem{s: s}, nil
}

// Read reads from the subsystem's standard output. An error carries
// anything the server wrote to standard error, and reports ctx's own
// error when the session's context is what ended it; a genuine end of
// stream is a plain, unwrapped io.EOF.
func (sub *Subsystem) Read(p []byte) (int, error) { return sub.s.read(p) }

// Write writes to the subsystem's standard input.
func (sub *Subsystem) Write(p []byte) (int, error) { return sub.s.write(p) }

// Stderr returns what the server has written to standard error,
// truncated to maxSubsystemStderrBytes and marked as such when it was.
// It may be called while a Read is in flight, in which case it returns
// what has arrived so far; after Close it is complete, because Close
// waits for the drain (see drainCloseGrace).
func (sub *Subsystem) Stderr() string { return sub.s.stderrText() }

// Close closes the underlying session and reaps this Subsystem's own
// goroutines. It is safe to call more than once; a Subsystem is not
// reusable afterward, matching Conn.Close's and Shell.Close's contract.
//
// It does NOT close the Conn: a Conn may carry several sessions, and
// tearing the connection down because one subsystem finished would
// break the reuse that is the whole reason Conn is exported.
func (sub *Subsystem) Close() error { return sub.s.close() }
