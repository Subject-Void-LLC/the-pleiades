package remoteexec

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// maxSubsystemStderrBytes bounds how much of a subsystem's standard
// error this package retains to explain a failure with. A subsystem
// server's diagnostics are a sentence or two ("subsystem request
// failed", a YANG validation complaint); anything past this is either a
// server misbehaving or a server trying to make this process hold its
// output in memory, and neither becomes more diagnosable at a hundred
// times the size. The bound follows ReadUntil's own maxBytes reasoning
// and pkg/catalystcenter/client.go's maxResponseBytes before it: a
// device is a trusted-ish upstream, but "trusted" is not "allowed to
// exhaust this process's memory."
const maxSubsystemStderrBytes = 8 << 10 // 8 KiB

// drainCloseGrace bounds how long Close waits for the standard error
// drain to finish after the session has been closed.
//
// The wait is what makes Stderr deterministic rather than racy: closing
// the session ends the stderr pipe, so the drain sees every byte the
// server sent and then returns, and a caller reading Stderr after Close
// gets a complete answer instead of whatever had happened to arrive.
// That matters most on the one path where stderr is the only
// explanation available, a refused subsystem request. The bound exists
// solely so a transport that somehow never ends that pipe cannot pin
// Close forever; in every ordinary case the drain has already returned
// and this waits for nothing.
const drainCloseGrace = 250 * time.Millisecond

// Subsystem is one live SSH subsystem channel on a Conn: the third
// session shape this package opens, after Run/RunWithStdin's one-shot
// exec request and Shell's interactive PTY. RFC 4254 section 6.5 makes a
// subsystem a named, structured service on the same "session" channel
// type the other two already use, so this costs a new SSH channel and
// nothing else, which is the whole reason it lives on Conn rather than
// behind a second dialer.
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
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader

	// ctx is retained so Read and Write can report the caller's own
	// cancellation as the cause, rather than the I/O failure that
	// closing the session out from under them produces, which describes
	// the mechanism rather than the cause. Retaining a context in a
	// struct is ordinarily discouraged; it is correct here because this
	// type's whole lifetime IS the scope ctx delimits, which is exactly
	// the case the guidance carves out.
	ctx context.Context

	// closeOnce guards both Close and the watchdog goroutine's exit, so
	// closing twice is safe and the goroutine is always reaped. drained
	// is closed by the stderr drain when it returns, which is what lets
	// Close wait for it.
	closeOnce sync.Once
	done      chan struct{}
	drained   chan struct{}

	// mu guards stderr and stderrTruncated, which the drain goroutine
	// writes and Stderr reads.
	mu              sync.Mutex
	stderr          []byte
	stderrTruncated bool

	// name and addr name the subsystem and the target in error
	// messages, mirroring Conn's own addr field of the same purpose.
	name string
	addr string
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
	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("remoteexec: open session on %s: %w", c.addr, err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open %s subsystem stdin on %s: %w", name, c.addr, err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open %s subsystem stdout on %s: %w", name, c.addr, err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open %s subsystem stderr on %s: %w", name, c.addr, err)
	}

	s := &Subsystem{
		session: session,
		stdin:   stdin,
		stdout:  stdout,
		ctx:     ctx,
		done:    make(chan struct{}),
		drained: make(chan struct{}),
		name:    name,
		addr:    c.addr,
	}

	// Started before RequestSubsystem, not after: a server that rejects
	// the request often explains why on standard error, and a drain
	// started only on success would miss exactly the bytes that make the
	// refusal diagnosable.
	go s.drainStderr(stderr)

	// The pipes are wired first so this watchdog covers the request
	// itself: RequestSubsystem blocks waiting for the server's reply,
	// and without this a caller's deadline could not reach a device that
	// accepts the channel and then never answers.
	go func() {
		select {
		case <-ctx.Done():
			// Not actionable here: this goroutine exists only to unblock
			// whatever call is waiting, which is what reports the failure.
			_ = s.session.Close() // #nosec G104 -- intentional, see comment above
		case <-s.done:
		}
	}()

	if err := session.RequestSubsystem(name); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("remoteexec: request %s subsystem on %s: %w%s",
			name, c.addr, wrapCtx(ctx, err), s.stderrSuffix())
	}

	return s, nil
}

// Read reads from the subsystem's standard output.
//
// An error is annotated with anything the server wrote to standard
// error, and reports ctx's own error when the session's context is what
// ended it. Without the first, a server that refuses a message and
// explains itself on standard error reaches the caller as a bare
// io.EOF; without the second, a passed deadline reaches the caller as
// whatever I/O failure closing the session produced.
func (s *Subsystem) Read(p []byte) (int, error) {
	n, err := s.stdout.Read(p)
	if err != nil && err != io.EOF {
		return n, fmt.Errorf("remoteexec: read from %s subsystem on %s: %w%s",
			s.name, s.addr, wrapCtx(s.ctx, err), s.stderrSuffix())
	}
	if err == io.EOF {
		// io.EOF is returned unwrapped, because callers and every
		// stdlib helper above this (bufio, io.ReadFull, encoding/xml's
		// own decoder) compare against it by identity. A ctx that ended
		// the session is still reported as such, since an EOF caused by
		// this package closing the session out from under the read is
		// not the stream genuinely ending.
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return n, fmt.Errorf("remoteexec: read from %s subsystem on %s: %w%s",
				s.name, s.addr, ctxErr, s.stderrSuffix())
		}
		return n, io.EOF
	}
	return n, err
}

// Write writes to the subsystem's standard input.
func (s *Subsystem) Write(p []byte) (int, error) {
	n, err := s.stdin.Write(p)
	if err != nil {
		return n, fmt.Errorf("remoteexec: write to %s subsystem on %s: %w%s",
			s.name, s.addr, wrapCtx(s.ctx, err), s.stderrSuffix())
	}
	return n, nil
}

// Stderr returns what the server has written to standard error,
// truncated to maxSubsystemStderrBytes and marked as such when it was.
// It may be called while a Read is in flight, in which case it returns
// what has arrived so far; after Close it is complete, because Close
// waits for the drain (see drainCloseGrace).
func (s *Subsystem) Stderr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stderrTruncated {
		return string(s.stderr) + "... (truncated)"
	}
	return string(s.stderr)
}

// Close closes the underlying session and reaps this Subsystem's own
// goroutines. It is safe to call more than once; a Subsystem is not
// reusable afterward, matching Conn.Close's and Shell.Close's contract.
//
// It does NOT close the Conn: a Conn may carry several sessions, and
// tearing the connection down because one subsystem finished would
// break the reuse that is the whole reason Conn is exported.
func (s *Subsystem) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		err = s.session.Close()
		// Closing the session ends the stderr pipe, so this returns as
		// soon as the drain has consumed what the server sent, which is
		// what makes a post-Close Stderr complete rather than racy.
		select {
		case <-s.drained:
		case <-time.After(drainCloseGrace):
		}
	})
	return err
}

// drainStderr copies the server's standard error into a bounded buffer
// until the stream ends, which happens when the session closes. Reading
// it continuously rather than on demand is what makes it available at
// the moment an error is built: an unread stderr pipe would also
// eventually apply back pressure to the SSH window and stall the
// session's standard output, turning a server's explanation into a hang.
func (s *Subsystem) drainStderr(r io.Reader) {
	defer close(s.drained)

	chunk := make([]byte, 1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			s.mu.Lock()
			room := maxSubsystemStderrBytes - len(s.stderr)
			if room > 0 {
				if n > room {
					s.stderr = append(s.stderr, chunk[:room]...)
					s.stderrTruncated = true
				} else {
					s.stderr = append(s.stderr, chunk[:n]...)
				}
			} else {
				s.stderrTruncated = true
			}
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// stderrSuffix renders whatever the server wrote to standard error as a
// clause to append to an error message, or the empty string when it
// wrote nothing, so an error reads naturally in both cases instead of
// ending in a dangling empty quote.
func (s *Subsystem) stderrSuffix() string {
	if msg := s.Stderr(); msg != "" {
		return fmt.Sprintf(" (server stderr: %q)", msg)
	}
	return ""
}
