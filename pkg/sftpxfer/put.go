// SFTP Put: an atomic replace through a private directory.
package sftpxfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// Put writes exactly size bytes from src to dst, replacing any existing
// file there atomically and setting its permission bits to mode. See
// the package doc for the sequence and why each step is there.
func (cl *Client) Put(ctx context.Context, dst filexfer.Path, src io.Reader, size int64, mode filexfer.Mode) (err error) {
	if err := filexfer.CheckPut(dst, size, mode); err != nil {
		return err
	}
	end, err := cl.begin(ctx, "put", dst)
	if err != nil {
		return err
	}
	defer end(&err)

	t, err := cl.confine(dst)
	if err != nil {
		return err
	}
	if err := t.leafKind(dst, false); err != nil {
		return err
	}
	// Refused before anything is created: a server that cannot rename
	// over an existing file would otherwise leave the new content
	// stranded in a temporary directory with the old file untouched.
	if t.info != nil && !cl.posixRename {
		return fmt.Errorf("%w: %q exists and the server does not offer %s",
			filexfer.ErrReplaceUnsupported, dst.String(), posixRenameExtension)
	}

	name, err := tempName()
	if err != nil {
		return err
	}
	tmpDir := path.Join(t.parent, name)
	tmpFile := path.Join(tmpDir, dst.Base())

	if err := cl.c.Mkdir(tmpDir); err != nil {
		return fmt.Errorf("sftpxfer: put %q: create a private directory: %w", dst.String(), err)
	}
	// Every failure from here on removes what was created. The removes
	// are best effort: the directory is private and its name says what
	// made it, and a cleanup failure must not hide the real error.
	committed := false
	defer func() {
		if !committed {
			_ = cl.c.Remove(tmpFile)
			_ = cl.c.RemoveDirectory(tmpDir)
		}
	}()

	// 0700 before any file exists inside, so no other account can open
	// the new file during the window its mode is still the server's
	// default: a descriptor opened in that window would survive a later
	// chmod.
	if err := cl.c.Chmod(tmpDir, 0o700); err != nil {
		return fmt.Errorf("sftpxfer: put %q: restrict the private directory: %w", dst.String(), err)
	}
	if err := cl.write(tmpFile, src, size, mode); err != nil {
		return fmt.Errorf("sftpxfer: put %q: %w", dst.String(), err)
	}

	if cl.posixRename {
		err = cl.c.PosixRename(tmpFile, t.path)
	} else {
		err = cl.c.Rename(tmpFile, t.path)
	}
	if err != nil {
		return fmt.Errorf("sftpxfer: put %q: rename into place: %w", dst.String(), err)
	}
	committed = true
	// The directory is empty now. A failure to remove it leaves an empty
	// private directory, which is not worth failing a transfer that has
	// already replaced its target.
	_ = cl.c.RemoveDirectory(tmpDir)
	return nil
}

// write creates file exclusively, streams exactly size bytes into it,
// and sets its mode on the open handle, so the mode is exact regardless
// of the server's umask.
func (cl *Client) write(file string, src io.Reader, size int64, mode filexfer.Mode) error {
	f, err := cl.c.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("create the temporary file: %w", err)
	}
	_, err = f.ReadFrom(filexfer.ExactReader(src, size))
	if err == nil {
		err = f.Chmod(mode.Perm())
	}
	if closeErr := f.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("close the temporary file: %w", closeErr)
	}
	return err
}
