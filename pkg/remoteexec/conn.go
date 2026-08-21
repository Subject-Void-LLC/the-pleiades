package remoteexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

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
// It is what lets a module write a file's contents to a device without
// this package growing a file-transfer protocol of its own: the caller
// runs a command that reads standard input and streams the bytes in. A
// nil stdin means the remote command sees an immediately-closed standard
// input, which is what Run passes.
func (c *Conn) RunWithStdin(ctx context.Context, command string, stdin io.Reader) (Result, error) {
	session, err := c.client.NewSession()
	if err != nil {
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
	// cannot un-merge them afterward.
	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

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
			return Result{}, fmt.Errorf("remoteexec: open stdin on %s: %w", c.addr, pipeErr)
		}
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

	// ssh.Session.Run takes no context, so cancellation is enforced by
	// closing the session out from under it, the same shape realDial uses
	// for the handshake. Without this a task's timeout could not reach a
	// remote command that has decided to run forever, and the device lock
	// that task holds would be pinned behind it. The goroutine exits as
	// soon as either ctx is done or Run returns, since done is closed by
	// the defer either way.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			// The error is not actionable here: this goroutine exists only
			// to unblock Run below, which is what reports the failure.
			_ = session.Close() // #nosec G104 -- intentional, see comment above
		case <-done:
		}
	}()

	runErr := session.Run(command)

	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}

	var exitErr *ssh.ExitError
	switch {
	case runErr == nil:
		result.ExitCode = 0

	case errors.As(runErr, &exitErr):
		// A non-zero remote exit status is real information, not a Go
		// error: the command ran to completion and reported failure.
		result.ExitCode = exitErr.ExitStatus()

	default:
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

	return result, nil
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
	var firstErr error
	for i := len(c.chain) - 1; i >= 0; i-- {
		if err := c.chain[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
