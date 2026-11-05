package remoteexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

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

	// raw, when set, sends each command as written for the login shell
	// rather than encoded for sh (Raw, script.go).
	raw bool

	// redial reconnects a detached command's polls (detach.go), and extra
	// holds the clients it dialed, closed with this Conn.
	redial redialer
	extra  []*ssh.Client

	// severVia, set for a connection opened through a route (ConnectVia),
	// cuts it from the route's first hop inward; a cancelled command's watch
	// calls it before closing the clients (conn_watch.go).
	severVia func()
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
// command is sent over the SSH exec channel for sh to read, whatever the
// login shell is (script.go), or as written on a Raw view; nothing is
// concatenated to it, and it never runs in a local shell. sh parses it,
// which is why QuoteArg and QuoteCommand exist: a caller building a command
// from untrusted parts must quote them, and this function will not do it
// silently.
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
//
// A context already ended sends nothing, as os/exec's Start refuses one. A
// context that ends while the command runs (cancelWatch) returns its own
// error, never an exit status or output, which the ending itself may have
// caused.
func (c *Conn) run(ctx context.Context, command string, stdin io.Reader, limit int) (Result, error) {
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	if err := c.usable(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, err)
	}
	// Refused here as well as in wire, since a detached command travels on
	// standard input and never reaches wire.
	if err := c.refuseNUL(command); err != nil {
		return Result{}, err
	}
	// An async task's command runs detached on the device (detach.go).
	if stdin == nil && detached(ctx) {
		return c.runDetached(ctx, command, limit)
	}
	line, err := c.wire(command)
	if err != nil {
		return Result{}, err
	}

	watch := c.watch(ctx)
	session, err := c.client.NewSession()
	if err != nil {
		cancelled := watch.finish()
		c.taint()
		if cancelled {
			return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, ctx.Err())
		}
		return Result{}, fmt.Errorf("remoteexec: open session on %s: %w", c.addr, err)
	}
	if !watch.attach(session) {
		// The context ended while the session was being opened.
		_ = session.Close()
		watch.finish()
		c.taint()
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, ctx.Err())
	}

	// release closes the session and then reaps the standard input copy:
	// closing it is what unblocks a copy still waiting on a remote that
	// stopped reading. It runs before the copy's result is read below, and
	// deferred for the early returns.
	var copying sync.WaitGroup
	var released sync.Once
	release := func() {
		released.Do(func() {
			_ = session.Close()
			copying.Wait()
		})
	}
	defer release()

	// Stdout and Stderr go into SEPARATE buffers, never CombinedOutput:
	// a caller that needs to tell an error message apart from real output
	// cannot un-merge them afterward. Past a limit, either buffer closes the
	// session so the remote stops writing (cappedBuffer).
	var exceeded atomic.Bool
	var cut sync.Once
	// A command cut off for its output is ended as a cancelled one is: sent
	// SIGTERM, its session closed, and its connection closed after the
	// abandon grace. A session close alone left the call waiting until the
	// command ended by itself (measured on OpenSSH 10.3: 20 s for a
	// "sleep 20" after its output passed the bound).
	cutSession := func() { cut.Do(watch.abort) }
	stdout := &cappedBuffer{limit: limit, exceeded: &exceeded, cut: cutSession}
	stderr := &cappedBuffer{limit: limit, exceeded: &exceeded, cut: cutSession}
	session.Stdout = stdout
	session.Stderr = stderr

	startCopy := func() {}
	var source *stdinSource
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
			cancelled := watch.finish()
			c.taint()
			if cancelled {
				return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, ctx.Err())
			}
			return Result{}, fmt.Errorf("remoteexec: open stdin on %s: %w", c.addr, pipeErr)
		}
		source = &stdinSource{r: stdin}
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
				// A short write is dropped: it means the remote stopped
				// reading, which says nothing about whether the command
				// succeeded. A failed READ of the caller's input is
				// different: sending the end of input then would let the
				// command take the part it got as the whole, so the command
				// is stopped instead and run reports the failure.
				_, _ = io.Copy(pipe, source)
				if source.failed() != nil {
					watch.abort()
					return
				}
				// No end of input once the command is being ended: the
				// caller's reader may finish after the cancellation, and an
				// end of input then is the one thing that would let the
				// command take what it got as the whole.
				if !watch.ending() {
					_ = pipe.Close()
				}
			}()
		}
	}

	runErr := withoutCommand(session.Start(line), line)
	if runErr == nil {
		startCopy()
		runErr = session.Wait()
	}
	cancelled := watch.finish()
	release()

	// The caller's own cancellation is reported as such, whatever the
	// session reported: an exit status from the signal, output cut short,
	// or the I/O failure closing the session produced, which describes the
	// mechanism rather than the cause and would send a reader looking for a
	// network fault that never happened.
	if cancelled {
		c.taint()
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, ctx.Err())
	}
	if readErr := source.failed(); readErr != nil {
		c.taint()
		return Result{}, fmt.Errorf("remoteexec: read the standard input of a command on %s, which was stopped and may have run in part: %w", c.addr, readErr)
	}

	// A command cut off for writing past its bound has an unknown outcome on
	// the remote side, like any other cut-off command, and its output was
	// not kept whole, so it is an error and never a truncated Result.
	if exceeded.Load() {
		c.taint()
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w (%d bytes on one stream)", c.addr, ErrOutputTooLarge, limit)
	}

	// The buffers are read only once Wait has returned: Wait is what joins
	// x/crypto/ssh's goroutines copying the streams into them.
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
		// handed to the next task. The command's true outcome on the remote
		// side is unknown, which is a genuine error and never an exit code.
		c.taint()
		return Result{}, fmt.Errorf("remoteexec: run command on %s: %w", c.addr, runErr)
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
