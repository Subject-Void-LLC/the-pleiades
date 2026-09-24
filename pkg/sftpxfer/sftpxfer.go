// Package sftpxfer moves a file's bytes to and from a device over SFTP,
// implementing the pkg/filexfer port (Store, and the optional Stater).
//
// # It dials nothing
//
// SFTP is an SSH subsystem (RFC 4254 section 6.5), so it rides an SSH
// connection this package does not open. The caller opens it through
// pkg/remoteexec, exactly as pkg/netconf's caller does for NETCONF:
//
//	sub, err := conn.Subsystem(ctx, "sftp")
//	client, err := sftpxfer.Open(ctx, sub)
//
// That is what makes the transfer inherit the connection's fail-closed
// host key verification, its dial retry, its per-target circuit breaker
// and its hop chain, with no second implementation of any of them. It
// is also what lets this package be tested over an in-memory pipe
// against a real SFTP server implementation, and then run over a real
// SSH channel in the release gate, through one identical code path.
// Open takes an io.ReadWriteCloser rather than an *ssh.Client for the
// same reason: pkg/remoteexec keeps its client unexported, so nothing
// can open a session that bypasses its context watchdog or its bounded
// standard error.
//
// # What a transfer checks before a content byte moves
//
// The Path it is handed was already confined lexically by
// filexfer.Resolve. Each call then works out where the root and the
// target's parent directory physically are, refuses a parent outside
// the root through filexfer.Contained, and refuses a final component
// that is a symlink, a directory or any other non-regular file. The
// transfer then operates on the physically resolved parent, never on
// the lexical path again.
//
// The physical answer is built on the client from LSTAT and READLINK,
// one component at a time, and never taken from the server's REALPATH.
// Not every server's REALPATH follows symlinks: pkg/sftp's own server
// answers it lexically, so a check that trusted it would pass exactly
// the escape it exists to catch. See confine.go.
//
// SFTP version 3 has no O_NOFOLLOW open and no openat, so a directory
// swapped for a symlink between the check and the open, by someone who
// can write inside the root, is not caught. The supported configuration
// is a transfer root that other local accounts cannot write to;
// docs/10-running-in-production.md discloses this.
//
// # Put replaces atomically, and its temporary file is private
//
// pkg/sftp creates files and directories with no permission attributes,
// so a new file starts at the server's default mode less its umask,
// commonly world-readable. Put therefore creates a private directory
// beside the target, restricts it to 0700 before any file exists inside
// it, writes the new content there, sets the requested mode on the open
// handle, and renames it over the target with the
// posix-rename@openssh.com extension, which replaces a file in one step.
// A reader of the target sees the old file or the new one, never a
// partial one. A server without that extension can still create a new
// file (plain SFTP rename refuses to overwrite), but replacing an
// existing one is refused with filexfer.ErrReplaceUnsupported rather
// than done in place.
//
// Replacing a file makes a new inode: the old file's owner, group, ACLs
// and extended attributes are not carried over, and a hard link to it
// keeps the old content.
package sftpxfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"

	"github.com/pkg/sftp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// posixRenameExtension is the OpenSSH extension that renames over an
// existing file atomically, which plain SFTP version 3 rename does not.
const posixRenameExtension = "posix-rename@openssh.com"

// tempDirPrefix names the private directory Put writes into before its
// rename. The leading dot keeps it out of an ordinary listing, and the
// name says what left it behind if an interrupted Put ever does.
const tempDirPrefix = ".pleiades-xfer-"

// Client is one SFTP session on one device. It is not safe for
// concurrent use by multiple goroutines, the same restriction the
// pkg/remoteexec channel under it carries.
type Client struct {
	c *sftp.Client

	// posixRename records whether the server advertised
	// posix-rename@openssh.com in its version reply, read once at Open.
	posixRename bool
}

var (
	_ filexfer.Store  = (*Client)(nil)
	_ filexfer.Stater = (*Client)(nil)
)

// Open starts an SFTP session over rwc, which is normally the result of
// (*remoteexec.Conn).Subsystem(ctx, "sftp"). The version handshake runs
// under ctx: if ctx ends first, rwc is closed and Open fails reporting
// ctx's own error. On success the Client owns rwc and Close closes it.
func Open(ctx context.Context, rwc io.ReadWriteCloser) (*Client, error) {
	if err := ctx.Err(); err != nil {
		_ = rwc.Close()
		return nil, fmt.Errorf("sftpxfer: open: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = rwc.Close() })
	c, err := sftp.NewClientPipe(rwc, rwc, sftp.UseConcurrentWrites(true))
	stop()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("sftpxfer: open: %w", ctxErr)
		}
		return nil, fmt.Errorf("sftpxfer: open: %w", err)
	}
	_, posix := c.HasExtension(posixRenameExtension)
	return &Client{c: c, posixRename: posix}, nil
}

// Close ends the SFTP session and closes the channel it ran on.
func (cl *Client) Close() error { return cl.c.Close() }

// begin ties one call to ctx. pkg/sftp's calls take no context, so the
// only way a deadline can reach one blocked on a silent server is to
// close the session out from under it: a canceled call leaves the
// Client closed for good, the same rule a pkg/remoteexec channel
// follows. The returned function must be deferred with the call's error
// pointer, so a failure caused by that close is reported as ctx's own
// error rather than as the I/O failure it produced.
func (cl *Client) begin(ctx context.Context, op string, p filexfer.Path) (func(*error), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sftpxfer: %s %q: %w", op, p.String(), err)
	}
	stop := context.AfterFunc(ctx, func() { _ = cl.c.Close() })
	return func(errp *error) {
		stop()
		if *errp != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				*errp = fmt.Errorf("sftpxfer: %s %q: %w", op, p.String(), ctxErr)
			}
		}
	}, nil
}

// kindOf maps an fs.FileMode's type bits to a filexfer.Kind.
func kindOf(m fs.FileMode) filexfer.Kind {
	switch {
	case m.IsRegular():
		return filexfer.KindRegular
	case m.IsDir():
		return filexfer.KindDirectory
	case m&fs.ModeSymlink != 0:
		return filexfer.KindSymlink
	default:
		return filexfer.KindOther
	}
}

// tempName returns a fresh, unguessable name for Put's private
// directory. It is unguessable so another account cannot pre-create it
// to steer the write, and fresh so two Puts into one directory never
// collide.
func tempName() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("sftpxfer: generating a temporary name: %w", err)
	}
	return tempDirPrefix + hex.EncodeToString(b[:]), nil
}
