// The engine host's files when that host is this machine (the local
// carrier).
package containerengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// localFS reaches this machine's own files.
type localFS struct{}

// close has nothing to release.
func (localFS) close() {}

// home is this process's home directory, the account a local session's
// engine was proved for (sameAccount).
func (localFS) home(context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("containerengine: finding this account's home directory: %w", err)
	}
	return home, nil
}

// regular returns what is at p when it is a regular file, false when
// nothing is there, and an error for anything else.
func regular(p string) (fs.FileInfo, bool, error) {
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("containerengine: %s is not a regular file", p)
	}
	return info, true, nil
}

// readSmall reads p whole when it is at most max bytes.
func (localFS) readSmall(_ context.Context, p string, max int) ([]byte, bool, error) {
	info, ok, err := regular(p)
	if err != nil || !ok {
		return nil, false, err
	}
	if info.Size() > int64(max) {
		return nil, false, fmt.Errorf("containerengine: %s is over the %d bytes this reads", p, max)
	}
	f, err := os.Open(p) // #nosec G304 -- p is a path the task named, checked by ValidHostPath; reading it is the method's purpose.
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	// One byte past the bound tells a file that grew since Lstat from one
	// that fits.
	data, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > max {
		return nil, false, fmt.Errorf("containerengine: %s is over the %d bytes this reads", p, max)
	}
	return data, true, nil
}

// writePrivate writes content to a 0600 temporary beside p and renames it
// over p.
func (localFS) writePrivate(_ context.Context, p string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("containerengine: making %s's directory: %w", p, err)
	}
	if _, _, err := regular(p); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".pleiades.*")
	if err != nil {
		return fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	// CreateTemp makes the file 0600; the rename keeps that mode.
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	if err := os.Rename(name, p); err != nil {
		return fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	return nil
}

// remove removes p.
func (localFS) remove(_ context.Context, p string) (bool, error) {
	if _, ok, err := regular(p); err != nil || !ok {
		return false, err
	}
	if err := os.Remove(p); err != nil {
		return false, fmt.Errorf("containerengine: removing %s: %w", p, err)
	}
	return true, nil
}

// digest hashes p.
func (localFS) digest(_ context.Context, p string) (string, bool, error) {
	if _, ok, err := regular(p); err != nil || !ok {
		return "", false, err
	}
	f, err := os.Open(p) // #nosec G304 -- p is a path the task named, checked by ValidHostPath.
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false, fmt.Errorf("containerengine: reading %s: %w", p, err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), true, nil
}

// open opens p for reading.
func (localFS) open(_ context.Context, p string) (io.ReadCloser, error) {
	_, ok, err := regular(p)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("containerengine: %s: %w", p, ErrHostFileAbsent)
	}
	return os.Open(p) // #nosec G304 -- p is a path the task named, checked by ValidHostPath.
}

// store writes r to a temporary beside p and renames it over p unless p
// already holds the same content.
func (l localFS) store(ctx context.Context, p string, r io.Reader) (Stored, error) {
	if _, _, err := regular(p); err != nil {
		return Stored{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".pleiades-archive.*")
	if err != nil {
		return Stored{}, fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Stored{}, fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	out := Stored{Digest: "sha256:" + hex.EncodeToString(h.Sum(nil)), Size: n}
	old, exists, err := l.digest(ctx, p)
	if err != nil {
		return Stored{}, err
	}
	if exists && old == out.Digest {
		return out, nil
	}
	if err := os.Rename(name, p); err != nil {
		return Stored{}, fmt.Errorf("containerengine: writing %s: %w", p, err)
	}
	out.Changed = true
	return out, nil
}
