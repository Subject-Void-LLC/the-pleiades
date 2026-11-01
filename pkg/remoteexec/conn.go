package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// Conn is one live, authenticated SSH connection to one device, on which
// the caller may run any number of commands. Close it when done.
//
// Reuse is the reason this type is exported rather than hidden inside
// Runner.Run. A module that has to look before it leaps (read a file's
// current checksum, then write it; read a service's state, then start
// it) needs two or three commands to decide whether it changed anything,
// and paying for a fresh TCP connect, key exchange and authentication
// round for each one turns a cheap idempotence check into the expensive
// part of the task.
//
// A Conn is not safe for concurrent use by multiple goroutines. The
// underlying SSH connection multiplexes channels perfectly well, but
// nothing here serializes two callers building sessions on it, and no
// caller in this codebase needs that today.
type Conn struct {
	// client is the final, target-reaching connection: what Run and
	// RunWithStdin actually open a session on.
	client *ssh.Client

	// chain is every client dialed to reach client, in dial order
	// (chain[0] is the first hop, or client itself when there are no
	// hops; chain[len(chain)-1] is always client). Close walks this in
	// REVERSE, because a hop's client owns the tunneled connection the
	// NEXT client in the chain is built on: closing hop 1 out from under
	// a still-open hop 2 (or the target) is what tears the whole chain
	// down cleanly, but doing it in dial order would sever a connection
	// while something is still layered on top of it.
	//
	// A zero-hop Conn still has a one-element chain (just client), so
	// Close needs no separate zero-hop case.
	chain []*ssh.Client

	// addr is kept only to name the target in error messages, so a
	// failure says which device it happened against.
	addr string

	// lease is set when this Conn was borrowed from a Pool: Close then
	// gives the connection back instead of tearing it down, and anything
	// that leaves the connection's state uncertain taints the lease so
	// the Pool closes it rather than handing it to the next task.
	lease *lease

	// become, when set, runs every command this view sends as another
	// account through sudo (WithBecome).
	become *Become

	// redial reconnects a detached command's polls (detach.go), and extra
	// holds the clients it dialed, closed with this Conn.
	redial redialer
	extra  []*ssh.Client
}

// taint marks a borrowed connection as not to be reused. It does nothing
// for a connection that was not borrowed.
func (c *Conn) taint() {
	if c.lease != nil {
		c.lease.taint()
	}
}

// usable refuses a borrowed Conn after its Close. A Conn that was really
// closed fails on its own, but a borrowed one's connection may by then be
// lent to another task, so without this a stale caller would run on it.
func (c *Conn) usable() error {
	if c.lease != nil && c.lease.isReleased() {
		return errReleased
	}
	return nil
}

// Run runs command on this connection and returns its output and exit
// code.
//
// command is sent VERBATIM over the SSH exec channel. Nothing here wraps
// it in a local shell or concatenates it with the target address. The
// remote sshd hands it to the login shell, which is why QuoteArg and
// QuoteCommand exist: a caller building a command from untrusted parts
// must quote them, and this function will not do it silently.
//
// A non-zero remote exit status is returned as Result.ExitCode with a
// nil error, because the command ran to completion and reported failure,
// which is information rather than a breakdown. Anything else (the
// connection dropping mid-command, a protocol error, the caller's ctx
// being canceled) is a real error, and Result is meaningless alongside
// it. Such a command is never retried: it may already have partially
// run, and re-sending it could apply an unknown side effect twice.
func (c *Conn) Run(ctx context.Context, command string) (Result, error) {
	return c.RunWithStdin(ctx, command, nil)
}

// RunWithStdin is Run with stdin piped to the remote command.
//
// Each of standard output and standard error is held to DefaultOutputLimit:
// a command that writes more is cut off and the call fails with
// ErrOutputTooLarge, never returning truncated output as if it were whole.
// RunWithStdinBounded takes a tighter bound; a caller that needs more than
// the default streams instead (Start).
//
// It is what lets a module write a file's contents to a device without
// this package growing a file-transfer protocol of its own: the caller
// runs a command that reads standard input and streams the bytes in. A
// nil stdin means the remote command sees an immediately-closed standard
// input, which is what Run passes.
func (c *Conn) RunWithStdin(ctx context.Context, command string, stdin io.Reader) (Result, error) {
	return c.run(ctx, command, stdin, 0)
}

// run is RunWithStdin and RunWithStdinBounded's one body. A limit above zero
// holds each of standard output and standard error to that many bytes
// (bounded.go); zero means the default bound, DefaultOutputLimit.
func (c *Conn) run(ctx context.Context, command string, stdin io.Reader, limit int) (Result, error) {
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	if err := c.usable(); err != nil {
		return Result{}, err
	}
	// An async task's command runs detached on the device (detach.go).
	if stdin == nil && detached(ctx) {
		return c.runDetached(ctx, command, limit)
	}
	session, err := c.client.NewSession()
	if err != nil {
		c.taint()
		return Result{}, fmt.Errorf("remoteexec: open session on %s: %w", c.addr, err)
	}

	// Registered before the session close below, so it runs after it:
	// closing the session is what unblocks a copy still waiting on a
	// remote that stopped reading, and this then reaps that goroutine
	// rather than leaving it running past the call.
	var copying sync.WaitGroup
	defer copying.Wait()
	defer func() { _ = session.Close() }()

	// Stdout and Stderr go into SEPARATE buffers, never CombinedOutput:
	// a caller that needs to tell an error message apart from real output
	// cannot un-merge them afterward. Past a limit, either buffer closes the
	// session so the remote stops writing (cappedBuffer).
	var exceeded atomic.Bool
	var cut sync.Once
	cutSession := func() { cut.Do(func() { _ = session.Close() }) }
	stdout := &cappedBuffer{limit: limit, exceeded: &exceeded, cut: cutSession}
	stderr := &cappedBuffer{limit: limit, exceeded: &exceeded, cut: cutSession}
	session.Stdout = stdout
	session.Stderr = stderr

	startCopy := func() {}
	if stdin != nil {
		// Copied through an explicit pipe rather than assigned to
		// session.Stdin, and the difference is not stylistic. With
		// session.Stdin, the library copies on our behalf and Wait
		// returns THAT COPY'S error whenever the command's own exit
		// status was clean. A command that exits successfully without
		// draining its input (anything that reads a header and stops,
		// or ignores stdin entirely) closes the channel while the copy is
		// still writing, and the copy's io.EOF then surfaces as a failed
		// command with its real exit status, stdout and stderr thrown
		// away. Measured: a "true" reading 512 KiB failed every time
		// while the same payload into "cat" succeeded, which is what
		// isolates it to unconsumed input rather than size.
		//
		// Owning the copy makes that error ours to ignore, which is
		// correct: a remote that stopped reading has not failed, it has
		// finished, and its exit status is the answer.
		pipe, pipeErr := session.StdinPipe()
		if pipeErr != nil {
			c.taint()
			return Result{}, fmt.Errorf("remoteexec: open stdin on %s: %w", c.addr, pipeErr)
		}
		// The copy starts once the command has (Start below): an end of
		// input sent before the exec request is never delivered to the
		// command by OpenSSH's sshd, so an empty stdin, whose close is
		// immediate, left "cat > file" waiting forever whenever the close
		// won the race (seen under -race on net.frr.config's backup of an
		// empty frr.conf, 2026-10-23).
		startCopy = func() {
			copying.Add(1)
			go func() {
				defer copying.Done()
				// Both errors are deliberately dropped. A short write means
				// the remote stopped reading, and closing an already-closed
				// pipe is the ordinary end of that. Neither says anything
				// about whether the command succeeded.
				_, _ = io.Copy(pipe, stdin)
				_ = pipe.Close()
			}()
		}
	}

	// ssh.Session.Run takes no context, so cancellation is enforced by
	// closing the session out from under it, the same shape realDial uses
	// for the handshake. Without this a task's timeout could not reach a
	// remote command that has decided to run forever, and the device lock
	// that task holds would be pinned behind it. The goroutine exits as
	// soon as either ctx is done or Run returns, since done is closed by
	// the defer either way.
	//
	// Closing the session alone leaves the command running on the device:
	// with no terminal, sshd sends it no hangup, so a task's timeout would
	// end the task and not the work. The command is sent SIGTERM first (an
	// SSH "signal" request, RFC 4254 section 6.9, which OpenSSH's sshd
	// delivers to the process it started). Processes that command started
	// in turn get nothing, and a server that ignores the request ends
	// nothing; the session is closed either way.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// The errors are not actionable here: this goroutine exists only
			// to unblock Run below, which is what reports the failure.
			_ = session.Signal(ssh.SIGTERM) // #nosec G104 -- intentional, see comment above
			_ = session.Close()             // #nosec G104 -- intentional, see comment above
		case <-done:
		}
	}()

	runErr := session.Start(c.elevate(command))
	if runErr == nil {
		startCopy()
		runErr = waitOrAbandon(ctx, session)
	}

	// A command cut off for writing past its bound has an unknown outcome on
	// the remote side, like any other cut-off command, and its output was
	// not kept whole, so it is an error and never a truncated Result.
	if exceeded.Load() {
		c.taint()
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w (%d bytes on one stream)", c.addr, ErrOutputTooLarge, limit)
	}

	// The buffers are read only once Wait has returned, in the two cases
	// below: Wait is what joins x/crypto/ssh's goroutines copying the
	// streams into them, and an abandoned command (waitOrAbandon gave up on
	// it) may still be writing, so reading them then would race.
	var exitErr *ssh.ExitError
	switch {
	case runErr == nil:
		return Result{Stdout: stdout.buf.String(), Stderr: stderr.buf.String()}, nil

	case errors.As(runErr, &exitErr):
		// A non-zero remote exit status is real information, not a Go
		// error: the command ran to completion and reported failure.
		return Result{Stdout: stdout.buf.String(), Stderr: stderr.buf.String(), ExitCode: exitErr.ExitStatus()}, nil

	default:
		// The connection's state is now unknown (a command cut off
		// mid-flight, or a transport failure), so a borrowed one is not
		// handed to the next task.
		c.taint()
		// Report the caller's own cancellation as such. The raw error from
		// Run in that case is whatever I/O failure resulted from the
		// goroutine above closing the session, which describes the
		// mechanism rather than the cause and would send a reader looking
		// for a network fault that never happened.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, ctxErr)
		}
		// Anything else means the command's true outcome on the remote
		// side is unknown, which is a genuine error and never an exit code.
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, runErr)
	}
}

// abandonGrace is how long a cancelled command's session is waited on after
// its SIGTERM and close before the call returns without it.
const abandonGrace = 2 * time.Second

// waitOrAbandon waits for session's command, or, once ctx ends, for at most
// abandonGrace more. OpenSSH's sshd answers a client's close only when the
// command's output ends, and may not deliver the signal (measured on Debian's
// sshd: "sleep 60 & wait" ran its whole minute past a three-second limit and
// reported success), so waiting on Wait alone let a task's limit pass
// unnoticed. An abandoned Wait ends when the connection closes; run taints the
// connection so it does.
func waitOrAbandon(ctx context.Context, session *ssh.Session) error {
	waited := make(chan error, 1)
	go func() { waited <- session.Wait() }()
	select {
	case err := <-waited:
		return err
	case <-ctx.Done():
	}
	select {
	case err := <-waited:
		return err
	case <-time.After(abandonGrace):
		return ctx.Err()
	}
}

// Close closes every connection in the chain that reaches this Conn's
// target, innermost (the target, or the last hop) first, walking back out
// to the first hop, so each layer shuts down cleanly before the
// connection tunneling it is torn away. It is safe to call once; a Conn
// is not reusable afterward.
//
// The first error encountered is returned, but every client is still
// closed regardless: a failure closing one connection must never leave
// an earlier hop in the chain leaked.
func (c *Conn) Close() error {
	// Clients a detached command redialed are this Conn's alone.
	for i := len(c.extra) - 1; i >= 0; i-- {
		_ = c.extra[i].Close()
	}
	if c.lease != nil {
		return c.lease.release()
	}
	return c.closeChain()
}

// closeChain closes every client in the chain, innermost first.
func (c *Conn) closeChain() error {
	var firstErr error
	for i := len(c.chain) - 1; i >= 0; i-- {
		if err := c.chain[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
