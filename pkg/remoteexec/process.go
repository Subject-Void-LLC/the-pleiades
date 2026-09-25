// A remote command started with its input and output kept open as a live
// stream, for protocols like legacy SCP.
package remoteexec

import (
	"context"

	"golang.org/x/crypto/ssh"
)

// Process is one remote command started on a Conn whose standard input
// and output stay open as a live byte stream, the fourth session shape
// this package opens after Run/RunWithStdin's one-shot exec, Shell's
// interactive PTY and Subsystem's named service. The names follow
// os/exec on purpose: Start begins a command without waiting for it,
// and Wait collects its exit status.
//
// It exists for a protocol that is a command underneath but a
// conversation on the wire. Legacy SCP is the first: the remote end runs
// "scp -t" or "scp -f", and the two sides then exchange a header, an
// acknowledgement byte, the file's bytes and another acknowledgement,
// each one waiting on the last. RunWithStdin cannot carry that, because
// it collects standard output into a buffer and hands it back only when
// the command has finished, which would both deadlock a protocol that
// waits for an acknowledgement and hold a multi-gigabyte download in
// memory. A Process hands both directions to the caller as they happen.
//
// It is an io.ReadWriteCloser plus exactly the two calls a started
// command needs to finish cleanly. CloseWrite sends end-of-file on
// standard input without ending the session, which is how a command
// that reads to end-of-file learns it has everything. Wait collects the
// exit status. Read standard output to end-of-file before calling Wait:
// a command blocked writing output nobody reads never exits.
//
// Standard error is separate from standard output, drained continuously
// into the same bounded buffer Subsystem uses, and reported by Stderr
// and in error text. The ctx passed to Start governs the whole session,
// exactly as it does for Subsystem: once it is done, the session is
// closed and every later Read, Write and Wait reports ctx's own error.
//
// A Process is not safe for concurrent use by multiple goroutines, with
// one deliberate exception: Stderr may be called while a Read is in
// flight.
type Process struct {
	// s is the shared stream implementation (stream.go), which Subsystem
	// uses too, so the two cannot drift in how they bound standard error
	// or honor a deadline.
	s *stream
}

// Start starts command on this connection and returns it as a live
// Process without waiting for it to finish. The caller must Close the
// result.
//
// command is sent VERBATIM over the SSH exec channel, exactly as Run
// sends it. The remote sshd hands it to the login shell, so a caller
// building a command from untrusted parts must quote every one of them
// with QuoteArg or QuoteCommand; this function will not do it silently.
//
// Like Subsystem, it shares c's already-authenticated *ssh.Client, so a
// started command inherits the host key verification, the dial retry,
// the circuit breaker and the hop chain the connection was built with,
// rather than a protocol package growing a second SSH dialer.
//
// Starting a command is never retried: it may already have partially
// run, and re-sending it could apply an unknown side effect twice.
func (c *Conn) Start(ctx context.Context, command string) (*Process, error) {
	// A streamed process is reused by nothing today, and one left running
	// would outlive its task, so a borrowed connection that started one
	// is closed rather than reused.
	c.taint()
	s, err := c.openStream(ctx, "remote command", func(session *ssh.Session) error {
		return session.Start(command)
	})
	if err != nil {
		return nil, err
	}
	return &Process{s: s}, nil
}

// Read reads from the command's standard output. An error carries
// anything the command wrote to standard error, and reports ctx's own
// error when the session's context is what ended it; a genuine end of
// output is a plain, unwrapped io.EOF.
func (p *Process) Read(b []byte) (int, error) { return p.s.read(b) }

// Write writes to the command's standard input.
func (p *Process) Write(b []byte) (int, error) { return p.s.write(b) }

// CloseWrite closes the command's standard input, which the command
// sees as end-of-file, while leaving its standard output open to be
// read to the end.
func (p *Process) CloseWrite() error { return p.s.closeWrite() }

// Wait waits for the command to exit and returns its exit status.
//
// A non-zero exit status is returned as a status with a nil error, the
// same rule Conn.Run follows through Result.ExitCode: the command ran to
// completion and reported failure, which is information rather than a
// breakdown. A session that ended with no exit status, a dropped
// connection, or ctx ending the session is a real error, and the status
// alongside it is -1. Calling Wait again returns the same answer.
func (p *Process) Wait() (int, error) { return p.s.wait() }

// Stderr returns what the command has written to standard error,
// truncated to the same 8 KiB bound Subsystem applies and marked as such
// when it was. After Close it is complete.
func (p *Process) Stderr() string { return p.s.stderrText() }

// Close closes the underlying session and reaps this Process's own
// goroutines. It is safe to call more than once, and it does NOT close
// the Conn, which may carry other sessions.
func (p *Process) Close() error { return p.s.close() }
