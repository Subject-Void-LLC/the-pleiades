// The engine host's files over the device's SSH login, through
// pkg/remotefile where it has the operation and plain POSIX commands, every
// operand quoted, where it does not.
package containerengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// run runs command and refuses a non-zero exit, naming what it was doing.
func (s *sshFS) run(ctx context.Context, what, command string) (string, error) {
	res, err := s.conn.Run(ctx, command)
	if err != nil {
		return "", fmt.Errorf("containerengine: %s: %w", what, err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("containerengine: %s: exited %d: %s", what, res.ExitCode, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	return res.Stdout, nil
}

// home asks the login shell for $HOME.
func (s *sshFS) home(ctx context.Context) (string, error) {
	out, err := s.run(ctx, "finding the login's home directory", `printf '%s' "$HOME"`)
	return out, err
}

// regular reads what is at p, refusing anything but a regular file or
// nothing.
func (s *sshFS) regular(ctx context.Context, p string) (remotefile.Info, error) {
	info, err := remotefile.Stat(ctx, s.conn, p)
	if err != nil {
		return info, fmt.Errorf("containerengine: reading %s: %w", p, err)
	}
	if info.Exists() && info.Kind != remotefile.KindFile {
		return info, fmt.Errorf("containerengine: %s is a %s, not a regular file", p, info.Kind)
	}
	return info, nil
}

// readSmall reads p through remotefile.ReadText.
func (s *sshFS) readSmall(ctx context.Context, p string, max int) ([]byte, bool, error) {
	info, err := s.regular(ctx, p)
	if err != nil || !info.Exists() {
		return nil, false, err
	}
	data, _, err := remotefile.ReadText(ctx, s.conn, p, max)
	if err != nil {
		return nil, false, fmt.Errorf("containerengine: reading %s: %w", p, err)
	}
	return data, true, nil
}

// writePrivate makes p's directory 0700 when it is missing, writes p
// through remotefile.Write (a mktemp temporary, which is 0600, renamed over
// p), and holds the mode to 0600 should anything have widened it.
func (s *sshFS) writePrivate(ctx context.Context, p string, content []byte) error {
	if _, err := s.regular(ctx, p); err != nil {
		return err
	}
	if _, err := s.run(ctx, "making "+path.Dir(p), "umask 077 && mkdir -p -- "+remoteexec.QuoteArg(path.Dir(p))); err != nil {
		return err
	}
	if err := remotefile.Write(ctx, s.conn, p, content); err != nil {
		return fmt.Errorf("containerengine: %w", err)
	}
	after, err := remotefile.Stat(ctx, s.conn, p)
	if err != nil {
		return fmt.Errorf("containerengine: reading %s after writing it: %w", p, err)
	}
	if _, err := remotefile.Apply(ctx, s.conn, p, remotefile.Attributes{Mode: "0600"}, after); err != nil {
		return fmt.Errorf("containerengine: %w", err)
	}
	return nil
}

// remove removes p through remotefile.Remove.
func (s *sshFS) remove(ctx context.Context, p string) (bool, error) {
	info, err := s.regular(ctx, p)
	if err != nil || !info.Exists() {
		return false, err
	}
	if err := remotefile.Remove(ctx, s.conn, p, false); err != nil {
		return false, fmt.Errorf("containerengine: %w", err)
	}
	return true, nil
}

// digest hashes p on the device (remotefile.Checksum).
func (s *sshFS) digest(ctx context.Context, p string) (string, bool, error) {
	if _, err := s.regular(ctx, p); err != nil {
		return "", false, err
	}
	sum, ok, err := remotefile.Checksum(ctx, s.conn, p)
	if err != nil || !ok {
		return "", false, err
	}
	return "sha256:" + sum, true, nil
}

// open streams p from `cat`, whose exit status is read at the end of the
// stream, so a read that failed partway is an error and not a short file.
func (s *sshFS) open(ctx context.Context, p string) (io.ReadCloser, error) {
	info, err := s.regular(ctx, p)
	if err != nil {
		return nil, err
	}
	if !info.Exists() {
		return nil, fmt.Errorf("containerengine: %s: %w", p, ErrHostFileAbsent)
	}
	proc, err := s.conn.Start(ctx, "cat -- "+remoteexec.QuoteArg(p))
	if err != nil {
		return nil, fmt.Errorf("containerengine: reading %s: %w", p, err)
	}
	if err := proc.CloseWrite(); err != nil {
		_ = proc.Close()
		return nil, fmt.Errorf("containerengine: reading %s: %w", p, err)
	}
	return &catReader{proc: proc, path: p}, nil
}

// catReader is a remote cat's output, ending with its exit status.
type catReader struct {
	proc *remoteexec.Process
	path string
}

// Read reads the file, turning the end of output into an error when cat
// did not exit 0.
func (c *catReader) Read(b []byte) (int, error) {
	n, err := c.proc.Read(b)
	if err != io.EOF {
		return n, err
	}
	code, waitErr := c.proc.Wait()
	if waitErr != nil {
		return n, fmt.Errorf("containerengine: reading %s: %w", c.path, waitErr)
	}
	if code != 0 {
		return n, fmt.Errorf("containerengine: reading %s: cat exited %d: %s", c.path, code, remoteexec.Excerpt(c.proc.Stderr(), 256))
	}
	return n, io.EOF
}

// Close ends the remote command.
func (c *catReader) Close() error { return c.proc.Close() }

// store writes r to a mktemp temporary beside p, hashing it as it goes,
// then renames it over p, or removes it when p already holds the same
// content.
func (s *sshFS) store(ctx context.Context, p string, r io.Reader) (Stored, error) {
	if _, err := s.regular(ctx, p); err != nil {
		return Stored{}, err
	}
	dir := path.Dir(p)
	out, err := s.run(ctx, "making a temporary in "+dir, "umask 077 && mktemp "+remoteexec.QuoteArg(dir+"/.pleiades-archive.XXXXXX"))
	if err != nil {
		return Stored{}, err
	}
	tmp := strings.TrimSpace(out)
	if path.Dir(tmp) != dir || !strings.HasPrefix(path.Base(tmp), ".pleiades-archive.") {
		return Stored{}, fmt.Errorf("containerengine: mktemp answered %q, not a temporary in %s", remoteexec.Excerpt(tmp, 128), dir)
	}
	keep := false
	defer func() {
		if !keep {
			cctx, cancel := CleanupContext(ctx)
			defer cancel()
			_, _ = s.conn.Run(cctx, "rm -f -- "+remoteexec.QuoteArg(tmp))
		}
	}()
	h := sha256.New()
	counted := &countingReader{r: io.TeeReader(r, h)}
	res, err := s.conn.RunWithStdin(ctx, "cat > "+remoteexec.QuoteArg(tmp), counted)
	if err != nil {
		return Stored{}, fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	if res.ExitCode != 0 {
		return Stored{}, fmt.Errorf("containerengine: writing %s: exited %d: %s", p, res.ExitCode, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	stored := Stored{Digest: "sha256:" + hex.EncodeToString(h.Sum(nil)), Size: counted.n}
	// The device's own hash of what landed, so a stream cut short on the
	// way is caught rather than renamed into place.
	landed, _, err := s.digest(ctx, tmp)
	if err != nil {
		return Stored{}, err
	}
	if landed != stored.Digest {
		return Stored{}, fmt.Errorf("containerengine: writing %s: the device holds %s, and %s was sent", p, landed, stored.Digest)
	}
	old, exists, err := s.digest(ctx, p)
	if err != nil {
		return Stored{}, err
	}
	if exists && old == stored.Digest {
		return stored, nil
	}
	if _, err := s.run(ctx, "renaming the archive into "+p, "mv -f -- "+remoteexec.QuoteArg(tmp)+" "+remoteexec.QuoteArg(p)); err != nil {
		return Stored{}, err
	}
	keep = true
	stored.Changed = true
	return stored, nil
}

// countingReader counts what passes through it.
type countingReader struct {
	r io.Reader
	n int64
}

// Read reads and counts.
func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += int64(n)
	return n, err
}
