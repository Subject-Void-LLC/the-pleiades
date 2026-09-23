// The one session-stream implementation Subsystem and Process share: pipes,
// a bounded stderr drain, and a context that governs the whole session.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// maxStreamStderrBytes bounds how much of a stream's standard error this
// package retains to explain a failure with. A server's diagnostics are
// a sentence or two ("subsystem request failed", a YANG validation
// complaint, "scp: permission denied"); anything past this is either a
// server misbehaving or a server trying to make this process hold its
// output in memory, and neither becomes more diagnosable at a hundred
// times the size. The bound follows ReadUntil's own maxBytes reasoning
// and pkg/catalystcenter/client.go's maxResponseBytes before it: a
// device is a trusted-ish upstream, but "trusted" is not "allowed to
// exhaust this process's memory."
const maxStreamStderrBytes = 8 << 10 // 8 KiB

// drainCloseGrace bounds how long close waits for the standard error
// drain to finish after the session has been closed.
//
// The wait is what makes stderr deterministic rather than racy: closing
// the session ends the stderr pipe, so the drain sees every byte the
// server sent and then returns, and a caller reading Stderr after Close
// gets a complete answer instead of whatever had happened to arrive.
// That matters most on the one path where stderr is the only
// explanation available, a refused subsystem request. The bound exists
// solely so a transport that somehow never ends that pipe cannot pin
// Close forever; in every ordinary case the drain has already returned
// and this waits for nothing.
const drainCloseGrace = 250 * time.Millisecond

// stream is the one implementation behind both Subsystem and Process:
// a live SSH session channel whose standard input and output stay open
// as a raw byte stream in both directions, whose standard error is
// drained into a bounded buffer, and whose whole lifetime is governed
// by one context.
//
// It is unexported so the two public types can keep their own names,
// documentation and method sets (a subsystem has no exit status worth
// waiting for, a started command does) while sharing every line that
// bounds stderr or honors a deadline. Two copies of that code would be
// two places for a future fix to miss.
//
// A stream is not safe for concurrent use by multiple goroutines, with
// one deliberate exception: the stderr drain runs in its own goroutine,
// so stderrText may be called while a read is in flight.
type stream struct {
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader

	// ctx is retained so read and write can report the caller's own
	// cancellation as the cause, rather than the I/O failure that
	// closing the session out from under them produces, which describes
	// the mechanism rather than the cause. Retaining a context in a
	// struct is ordinarily discouraged; it is correct here because this
	// type's whole lifetime IS the scope ctx delimits, which is exactly
	// the case the guidance carves out.
	ctx context.Context

	// closeOnce guards both close and the watchdog goroutine's exit, so
	// closing twice is safe and the goroutine is always reaped. drained
	// is closed by the stderr drain when it returns, which is what lets
	// close wait for it.
	closeOnce sync.Once
	done      chan struct{}
	drained   chan struct{}

	// waitOnce caches the one answer ssh.Session.Wait can give, because
	// a second call to it would block forever waiting for an exit status
	// the server only ever sends once.
	waitOnce sync.Once
	exitCode int
	waitErr  error

	// mu guards stderr and stderrTruncated, which the drain goroutine
	// writes and stderrText reads.
	mu              sync.Mutex
	stderr          []byte
	stderrTruncated bool

	// desc and addr name the stream ("netconf subsystem", "remote
	// command") and the target in error messages, mirroring Conn's own
	// addr field of the same purpose. A started command's text is
	// deliberately NOT part of desc: it can carry a remote path or other
	// runbook-authored value, and an error message is not where that
	// belongs.
	desc string
	addr string
}

// openStream opens one session on c, wires its three pipes, starts the
// stderr drain and the ctx watchdog, and only then calls request, which
// issues whatever gives the session its purpose (a subsystem request,
// or an exec request). It is the one construction path Subsystem and
// Start share, so the two cannot drift in how they bound stderr or
// honor a deadline.
func (c *Conn) openStream(ctx context.Context, desc string, request func(*ssh.Session) error) (*stream, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("remoteexec: open session on %s: %w", c.addr, err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open %s stdin on %s: %w", desc, c.addr, err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open %s stdout on %s: %w", desc, c.addr, err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("remoteexec: open %s stderr on %s: %w", desc, c.addr, err)
	}

	s := &stream{
		session: session,
		stdin:   stdin,
		stdout:  stdout,
		ctx:     ctx,
		done:    make(chan struct{}),
		drained: make(chan struct{}),
		desc:    desc,
		addr:    c.addr,
	}

	// Started before the request, not after: a server that rejects the
	// request often explains why on standard error, and a drain started
	// only on success would miss exactly the bytes that make the refusal
	// diagnosable.
	go s.drainStderr(stderr)

	// The pipes are wired first so this watchdog covers the request
	// itself: the request blocks waiting for the server's reply, and
	// without this a caller's deadline could not reach a device that
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

	if err := request(session); err != nil {
		_ = s.close()
		return nil, fmt.Errorf("remoteexec: request %s on %s: %w%s",
			desc, c.addr, wrapCtx(ctx, err), s.stderrSuffix())
	}

	return s, nil
}

// read reads from the session's standard output.
//
// An error is annotated with anything the server wrote to standard
// error, and reports ctx's own error when the session's context is what
// ended it. Without the first, a server that refuses a message and
// explains itself on standard error reaches the caller as a bare
// io.EOF; without the second, a passed deadline reaches the caller as
// whatever I/O failure closing the session produced.
func (s *stream) read(p []byte) (int, error) {
	n, err := s.stdout.Read(p)
	if err != nil && err != io.EOF {
		return n, fmt.Errorf("remoteexec: read from %s on %s: %w%s",
			s.desc, s.addr, wrapCtx(s.ctx, err), s.stderrSuffix())
	}
	if err == io.EOF {
		// io.EOF is returned unwrapped, because callers and every
		// stdlib helper above this (bufio, io.ReadFull, encoding/xml's
		// own decoder) compare against it by identity. A ctx that ended
		// the session is still reported as such, since an EOF caused by
		// this package closing the session out from under the read is
		// not the stream genuinely ending.
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return n, fmt.Errorf("remoteexec: read from %s on %s: %w%s",
				s.desc, s.addr, ctxErr, s.stderrSuffix())
		}
		return n, io.EOF
	}
	return n, err
}

// write writes to the session's standard input.
func (s *stream) write(p []byte) (int, error) {
	n, err := s.stdin.Write(p)
	if err != nil {
		return n, fmt.Errorf("remoteexec: write to %s on %s: %w%s",
			s.desc, s.addr, wrapCtx(s.ctx, err), s.stderrSuffix())
	}
	return n, nil
}

// closeWrite closes the session's standard input, which the server sees
// as end-of-file, while leaving standard output open to be read.
func (s *stream) closeWrite() error {
	if err := s.stdin.Close(); err != nil {
		return fmt.Errorf("remoteexec: close %s stdin on %s: %w",
			s.desc, s.addr, wrapCtx(s.ctx, err))
	}
	return nil
}

// wait waits for the remote side to exit and returns its exit status.
//
// A non-zero exit status is returned as a status with a nil error, the
// same rule Conn.Run follows through Result.ExitCode: the remote side
// ran to completion and reported failure, which is information rather
// than a breakdown. Anything else (a session that ended with no exit
// status, a dropped connection, ctx ending the session) is a real error.
// The answer is computed once and cached, so calling wait again is safe
// and returns the same result.
func (s *stream) wait() (int, error) {
	s.waitOnce.Do(func() {
		err := s.session.Wait()
		var exitErr *ssh.ExitError
		switch {
		case err == nil:
			s.exitCode = 0
		case errors.As(err, &exitErr):
			s.exitCode = exitErr.ExitStatus()
		default:
			s.exitCode = -1
			s.waitErr = fmt.Errorf("remoteexec: wait for %s on %s: %w%s",
				s.desc, s.addr, wrapCtx(s.ctx, err), s.stderrSuffix())
		}
	})
	return s.exitCode, s.waitErr
}

// stderrText returns what the server has written to standard error,
// truncated to maxStreamStderrBytes and marked as such when it was. It
// may be called while a read is in flight, in which case it returns what
// has arrived so far; after close it is complete, because close waits
// for the drain (see drainCloseGrace).
func (s *stream) stderrText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stderrTruncated {
		return string(s.stderr) + "... (truncated)"
	}
	return string(s.stderr)
}

// close closes the underlying session and reaps this stream's own
// goroutines. It is safe to call more than once.
//
// It does NOT close the Conn: a Conn may carry several sessions, and
// tearing the connection down because one stream finished would break
// the reuse that is the whole reason Conn is exported.
func (s *stream) close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		err = s.session.Close()
		// Closing the session ends the stderr pipe, so this returns as
		// soon as the drain has consumed what the server sent, which is
		// what makes a post-close stderrText complete rather than racy.
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
func (s *stream) drainStderr(r io.Reader) {
	defer close(s.drained)

	chunk := make([]byte, 1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			s.mu.Lock()
			room := maxStreamStderrBytes - len(s.stderr)
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
func (s *stream) stderrSuffix() string {
	if msg := s.stderrText(); msg != "" {
		return fmt.Sprintf(" (server stderr: %q)", msg)
	}
	return ""
}
