// Package scpxfer moves a file's bytes to and from a device over legacy
// SCP, implementing the pkg/filexfer port's Store. It does not
// implement filexfer.Stater: the SCP protocol has no way to describe a
// file without sending it.
//
// # Why legacy SCP at all
//
// OpenSSH's own scp client has spoken SFTP by default since 9.0, so a
// device that serves SFTP should be reached through pkg/sftpxfer. Legacy
// SCP exists for the devices that serve only "scp -t" and "scp -f",
// which the network world still has plenty of. The protocol is a command
// underneath (the device runs scp in sink or source mode) and a
// conversation on the wire, so it rides pkg/remoteexec's Conn.Start,
// which keeps both directions open while the command runs, on a
// connection that already carries host key verification, the dial
// retry, the circuit breaker and the hop chain.
//
// # One script per transfer, and the client decides
//
// Each transfer is one exec of a small POSIX shell script on the
// device. Before any content moves it resolves the transfer root and
// the target's parent directory physically (the kernel's own "cd -P"
// and "pwd -P"), leaves the shell standing in that physical directory,
// describes the final component without following it, and prints those
// answers. The client compares them with filexfer.Contained and
// filexfer.LeafKind, the same functions the SFTP adapter uses, so the
// containment rule exists once, in Go, where it is fuzzed. Only on the
// client's "y" does the script run scp; on anything else it exits
// having changed nothing.
//
// Everything after the check happens relative to the directory the
// shell is standing in, so a directory swapped for a symlink after the
// check cannot redirect the write: the shell's working directory is the
// already resolved inode, not a path looked up again. What remains is a
// final component replaced between the check and the scp open by
// someone who can write inside the root, the same residual the SFTP
// adapter has; the supported configuration is a root other local
// accounts cannot write to.
//
// A Put writes into a private directory made by mktemp -d (0700 from
// the moment it exists), sets the requested mode exactly with chmod, and
// renames the file into place with mv -f, which replaces the target in
// one step. A Get writes only to the caller's io.Writer and checks, but
// never uses, the file name the device sends back, so a hostile server
// cannot choose where anything lands (the CVE-2019-6111 class of attack
// on scp clients).
//
// # What the device must provide
//
// A POSIX /bin/sh with "cd -P", "pwd -P", "[", "printf" and "read",
// plus mktemp, chmod, mv and scp. That is every Linux and BSD server;
// it is not a Cisco IOS device, whose SCP server has no shell behind
// it. Supporting one means a second preflight that answers the same
// three questions its own way ("flash:" has no symlinks to follow), not
// a change to the rule.
package scpxfer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Client moves files over legacy SCP on one connection. It holds no
// session of its own: each Put or Get starts and finishes one remote
// command, so a Client is as safe for sequential reuse as the Conn under
// it, and like that Conn it is not safe for concurrent use.
type Client struct {
	conn *remoteexec.Conn
}

var _ filexfer.Store = (*Client)(nil)

// New returns a Client that transfers over conn. The caller keeps
// ownership of conn and closes it.
func New(conn *remoteexec.Conn) *Client {
	return &Client{conn: conn}
}

// transfer is one running device-side script and the reader its
// standard output is consumed through.
type transfer struct {
	proc *remoteexec.Process
	br   *bufio.Reader
	path filexfer.Path
}

// start runs script, reads its preflight answers, and judges them. It
// returns a transfer only when the client may send the "y"; on a refusal
// it has already sent the "n", so the script exits having done nothing.
func (cl *Client) start(ctx context.Context, p filexfer.Path, script string, forGet bool) (*transfer, error) {
	proc, err := cl.conn.Start(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("scpxfer: %q: %w", p.String(), err)
	}
	t := &transfer{proc: proc, br: bufio.NewReader(proc), path: p}

	ans, err := readPreflight(t.br)
	if err != nil {
		return nil, t.fail(ctx, fmt.Errorf("reading the preflight answer: %w", err))
	}
	verdict := filexfer.Contained(p, ans.root, ans.parent)
	if verdict == nil {
		verdict = filexfer.LeafKind(p, ans.kind, ans.exists, forGet)
	}
	if verdict != nil {
		_, _ = io.WriteString(proc, "n\n")
		_, _ = t.finish()
		return nil, verdict
	}
	if _, err := io.WriteString(proc, "y\n"); err != nil {
		return nil, t.fail(ctx, fmt.Errorf("approving the transfer: %w", err))
	}
	return t, nil
}

// fail ends a transfer that went wrong and chooses the most useful
// explanation, in this order: the caller's own cancellation; a size or
// limit refusal, which is the caller's own sentinel to test for; a
// refusal the script made before any content moved (a missing root or
// parent directory), which the protocol error merely surfaced; and
// otherwise the protocol error, with the script's own verdict beside it.
//
// It waits for the script's exit status only when the script's output
// has ended. A device still streaming a file nobody will read would
// otherwise block in its write forever and take this call with it, so
// in that case the session is closed out from under it instead.
func (t *transfer) fail(ctx context.Context, cause error) error {
	_ = t.proc.CloseWrite()
	drained, _ := io.Copy(io.Discard, io.LimitReader(t.br, maxRecordBytes+1))
	code := -1
	if drained <= maxRecordBytes {
		code, _ = t.proc.Wait()
	}
	_ = t.proc.Close()

	switch {
	case ctx.Err() != nil:
		return fmt.Errorf("scpxfer: %q: %w", t.path.String(), ctx.Err())
	case errors.Is(cause, filexfer.ErrSizeMismatch), errors.Is(cause, filexfer.ErrLimitExceeded):
		return fmt.Errorf("scpxfer: %q: %w", t.path.String(), cause)
	case code >= exitRootMissing && code <= exitNoTempDir:
		return exitError(t.path, code, t.proc.Stderr())
	case code >= exitScpFailed && code <= exitRenameFailed:
		return fmt.Errorf("%w (%v)", exitError(t.path, code, t.proc.Stderr()), cause)
	}
	return fmt.Errorf("scpxfer: %q: %w", t.path.String(), cause)
}

// finish closes the script's input, waits for it, and returns its exit
// status, which the caller maps to an error.
func (t *transfer) finish() (int, error) {
	_ = t.proc.CloseWrite()
	code, err := t.proc.Wait()
	_ = t.proc.Close()
	return code, err
}

// Put writes exactly size bytes from src to dst, replacing any existing
// file there atomically and setting its permission bits to mode.
func (cl *Client) Put(ctx context.Context, dst filexfer.Path, src io.Reader, size int64, mode filexfer.Mode) error {
	if err := filexfer.CheckPut(dst, size, mode); err != nil {
		return err
	}
	t, err := cl.start(ctx, dst, putScript(dst, mode), false)
	if err != nil {
		return err
	}
	if err := send(t.br, t.proc, src, size, dst.Base()); err != nil {
		return t.fail(ctx, err)
	}
	// The sink exits on end of input; the script then sets the mode and
	// renames, and prints nothing more.
	if err := t.proc.CloseWrite(); err != nil {
		return t.fail(ctx, err)
	}
	if extra, _ := io.ReadAll(io.LimitReader(t.br, maxRecordBytes)); len(extra) != 0 {
		return t.fail(ctx, fmt.Errorf("unexpected output after the transfer: %q", extra))
	}
	code, err := t.finish()
	if err != nil {
		return fmt.Errorf("scpxfer: put %q: %w", dst.String(), err)
	}
	if code != 0 {
		return exitError(dst, code, t.proc.Stderr())
	}
	return nil
}

// Get copies the regular file at src to dst, refusing a file larger
// than limit before reading a byte of it.
func (cl *Client) Get(ctx context.Context, src filexfer.Path, dst io.Writer, limit int64) (int64, error) {
	if err := filexfer.CheckGet(src, limit); err != nil {
		return 0, err
	}
	t, err := cl.start(ctx, src, getScript(src), true)
	if err != nil {
		return 0, err
	}
	n, err := receive(t.br, t.proc, dst, src.Base(), limit)
	if err != nil {
		return n, t.fail(ctx, err)
	}
	code, err := t.finish()
	if err != nil {
		return n, fmt.Errorf("scpxfer: get %q: %w", src.String(), err)
	}
	if code != 0 {
		return n, exitError(src, code, t.proc.Stderr())
	}
	return n, nil
}
