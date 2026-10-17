// Files on the machine an engine runs on: an image archive a method saves
// or loads, and the registry login a method writes for the engine's own
// later pulls.
package containerengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ErrHostFileAbsent is what HostFiles.Open reports for a path with nothing
// at it, so a caller can tell a missing archive from a failed read.
var ErrHostFileAbsent = errors.New("no file is there")

// Stored is what HostFiles.Store wrote.
type Stored struct {
	// Digest is the content's SHA-256, "sha256:<hex>".
	Digest string
	// Size is the content's length in bytes.
	Size int64
	// Changed reports that the file at the path is new or now different:
	// content identical to what was there leaves the file as it was.
	Changed bool
}

// hostFS is one way to reach the engine host's files: this machine's own
// (the local carrier) or the device's over an SSH login (every other).
type hostFS interface {
	// home is the login account's home directory.
	home(ctx context.Context) (string, error)
	// readSmall reads a regular file of at most max bytes, reporting false
	// for an absent one.
	readSmall(ctx context.Context, p string, max int) ([]byte, bool, error)
	// writePrivate replaces p atomically with content, mode 0600, making
	// any missing parent directory with mode 0700.
	writePrivate(ctx context.Context, p string, content []byte) error
	// remove removes the file at p, reporting false when it was absent.
	remove(ctx context.Context, p string) (bool, error)
	// digest is the SHA-256 of the regular file at p, false when absent.
	digest(ctx context.Context, p string) (string, bool, error)
	// open streams the regular file at p.
	open(ctx context.Context, p string) (io.ReadCloser, error)
	// store streams r into p through a private temporary beside it.
	store(ctx context.Context, p string, r io.Reader) (Stored, error)
	// close releases what reaching the files opened.
	close()
}

// HostFiles reaches files on the machine a device's engine runs on, the
// same machine a session reaches: through this process's own file system
// when the engine is reached through its local socket, and over the
// device's SSH login otherwise. Close it when done.
type HostFiles struct {
	fs hostFS
	// Local reports that the files are this machine's own.
	Local bool
}

// OpenHostFiles reaches the files of the machine device's engine runs on,
// for the task named fqcn. The carrier is the one a session would take
// (Route); a local one is allowed only where AllowLocalCarrier was called,
// exactly as for a session.
func OpenHostFiles(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*HostFiles, error) {
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: a container method addresses a specific container host, set the task's target", fqcn)
	}
	_, carrier, err := Route(device, params, "")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	if carrier == capability.CarrierLocal {
		if !localCarrier.Load() {
			return nil, refuseLocalCarrier(fqcn, device.Name())
		}
		return &HostFiles{fs: localFS{}, Local: true}, nil
	}
	// Every other carrier reaches a machine this process is not: its files
	// are reached over the device's SSH login, the one a session's socket
	// and dial-stdio carriers ride. A device reached by TLS alone has none,
	// and sdk.Connect's refusal says so.
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}
	return &HostFiles{fs: &sshFS{conn: conn}}, nil
}

// Close releases what OpenHostFiles opened.
func (h *HostFiles) Close() { h.fs.close() }

// Home returns the login account's home directory on the host, absolute
// and clean.
func (h *HostFiles) Home(ctx context.Context) (string, error) {
	home, err := h.fs.home(ctx)
	if err != nil {
		return "", err
	}
	if err := ValidHostPath("home directory", home); err != nil {
		return "", err
	}
	return home, nil
}

// ReadSmall reads the regular file at p, of at most max bytes, reporting
// false when nothing is there.
func (h *HostFiles) ReadSmall(ctx context.Context, p string, max int) ([]byte, bool, error) {
	if err := ValidHostPath("path", p); err != nil {
		return nil, false, err
	}
	return h.fs.readSmall(ctx, p, max)
}

// WritePrivate replaces the file at p with content, atomically and with
// mode 0600 from the moment it exists, making a missing parent directory
// with mode 0700. It is for a file holding a secret.
func (h *HostFiles) WritePrivate(ctx context.Context, p string, content []byte) error {
	if err := ValidHostPath("path", p); err != nil {
		return err
	}
	return h.fs.writePrivate(ctx, p, content)
}

// Remove removes the file at p, reporting false when nothing was there.
func (h *HostFiles) Remove(ctx context.Context, p string) (bool, error) {
	if err := ValidHostPath("path", p); err != nil {
		return false, err
	}
	return h.fs.remove(ctx, p)
}

// Digest is the SHA-256 of the regular file at p, "sha256:<hex>", and
// false when nothing is there.
func (h *HostFiles) Digest(ctx context.Context, p string) (string, bool, error) {
	if err := ValidHostPath("path", p); err != nil {
		return "", false, err
	}
	return h.fs.digest(ctx, p)
}

// Open streams the regular file at p. An absent one is ErrHostFileAbsent;
// a read that fails partway is an error from Read, never an early end.
func (h *HostFiles) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	if err := ValidHostPath("path", p); err != nil {
		return nil, err
	}
	return h.fs.open(ctx, p)
}

// Store streams r into the file at p: written whole to a temporary of mode
// 0600 in p's directory, hashed as it is written, then renamed over p, or
// discarded when p already holds the same content. A reader of p sees the
// old file or the new one, never part of one.
func (h *HostFiles) Store(ctx context.Context, p string, r io.Reader) (Stored, error) {
	if err := ValidHostPath("path", p); err != nil {
		return Stored{}, err
	}
	return h.fs.store(ctx, p, r)
}

// ValidHostPath refuses a path on the engine host that is not absolute and
// clean, or that is the root itself: what reaches a file there is exactly
// the path the task named.
func ValidHostPath(what, p string) error {
	switch {
	case p == "":
		return fmt.Errorf("containerengine: the %s is empty", what)
	case !strings.HasPrefix(p, "/"):
		return fmt.Errorf("containerengine: %s %q is not absolute", what, p)
	case path.Clean(p) != p:
		return fmt.Errorf("containerengine: %s %q is not clean (it would be %q)", what, p, path.Clean(p))
	case p == "/":
		return fmt.Errorf("containerengine: %s %q is the root directory", what, p)
	case strings.ContainsAny(p, "\x00\n\r"):
		return fmt.Errorf("containerengine: %s %q holds a control character", what, p)
	}
	return nil
}

// sshFS reaches the host's files over an SSH login.
type sshFS struct {
	conn *remoteexec.Conn
}

// close returns the login.
func (s *sshFS) close() { _ = s.conn.Close() }
