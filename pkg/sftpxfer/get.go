// SFTP Get and Stat.
package sftpxfer

import (
	"context"
	"fmt"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// Get copies the regular file at src to dst, refusing a file larger
// than limit before reading a byte of it, and stopping at limit if the
// file grows while it is being read.
func (cl *Client) Get(ctx context.Context, src filexfer.Path, dst io.Writer, limit int64) (n int64, err error) {
	if err := filexfer.CheckGet(src, limit); err != nil {
		return 0, err
	}
	end, err := cl.begin(ctx, "get", src)
	if err != nil {
		return 0, err
	}
	defer end(&err)

	t, err := cl.confine(src)
	if err != nil {
		return 0, err
	}
	if err := t.leafKind(src, true); err != nil {
		return 0, err
	}
	if size := t.info.Size(); size > limit {
		return 0, fmt.Errorf("%w: %q is %d bytes, over the limit of %d",
			filexfer.ErrLimitExceeded, src.String(), size, limit)
	}

	f, err := cl.c.Open(t.path)
	if err != nil {
		return 0, fmt.Errorf("sftpxfer: get %q: %w", src.String(), err)
	}
	defer f.Close()

	n, err = f.WriteTo(filexfer.LimitWriter(dst, limit))
	if err != nil {
		return n, fmt.Errorf("sftpxfer: get %q: %w", src.String(), err)
	}
	return n, nil
}

// Stat describes the file at p without following a symlink there. The
// same containment check a transfer makes runs first, so Stat cannot be
// used to probe outside the root either.
func (cl *Client) Stat(ctx context.Context, p filexfer.Path) (info filexfer.Info, err error) {
	if p.IsZero() {
		return filexfer.Info{}, filexfer.ErrUnresolvedPath
	}
	end, err := cl.begin(ctx, "stat", p)
	if err != nil {
		return filexfer.Info{}, err
	}
	defer end(&err)

	t, err := cl.confine(p)
	if err != nil {
		return filexfer.Info{}, err
	}
	if t.info == nil {
		return filexfer.Info{}, filexfer.LeafKind(p, 0, false, true)
	}
	return filexfer.Info{
		Size:    t.info.Size(),
		Mode:    filexfer.Mode(t.info.Mode().Perm()),
		Kind:    kindOf(t.info.Mode()),
		ModTime: t.info.ModTime(),
	}, nil
}
