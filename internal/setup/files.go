// The one directory setup writes into, confined with os.Root, with exclusive
// creates and compare-and-swap replaces.
package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// maxFileBytes caps a file this command reads back. An env file or a
// manifest this command wrote is a few hundred bytes; the cap stops a
// mistaken path from reading something enormous.
const maxFileBytes = 1 << 20

// ErrChangedUnderneath is returned when a file this command is about to
// write was created or changed by something else after it looked.
var ErrChangedUnderneath = errors.New("setup: the file changed while setup was running")

// Dir is the one directory this command writes into.
//
// It is opened as an os.Root, which is the whole of this command's path
// hardening: every name is resolved inside the directory and cannot escape
// it, whether through "..", an absolute path, or a symbolic link planted
// along the way. The names this command writes are fixed constants, so the
// only path an operator supplies is the directory itself.
type Dir struct {
	root    *os.Root
	display string
}

// OpenDir opens path for reading and writing. display is how the directory
// is named in messages, which differs from path when the command runs in a
// container with the operator's directory mounted somewhere else.
func OpenDir(path, display string) (*Dir, error) {
	if err := checkPrintable(path); err != nil {
		return nil, err
	}
	if display == "" {
		display = path
	}
	if err := checkPrintable(display); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("setup: cannot open the directory %s: %w", display, err)
	}
	return &Dir{root: root, display: display}, nil
}

// checkPrintable refuses a path holding a control character. Such a path
// is never what an operator meant, and printing one back in a message could
// move the cursor or rewrite the line a person is reading.
func checkPrintable(path string) error {
	for _, r := range path {
		if unicode.IsControl(r) {
			return errors.New("setup: the directory path contains a control character")
		}
	}
	return nil
}

// Close releases the directory.
func (d *Dir) Close() error { return d.root.Close() }

// Show names a file in the directory the way a message should.
func (d *Dir) Show(name string) string { return filepath.Join(d.display, name) }

// ReadExisting reads name if it exists, and reports whether it did.
//
// It refuses a symbolic link: the file a link points at is not the file this
// command was asked to write, and following one is how a planted link turns
// a setup run into a write somewhere else. It refuses anything that is not a
// regular file, and anything larger than maxFileBytes.
func (d *Dir) ReadExisting(name string) ([]byte, bool, error) {
	info, err := d.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("setup: cannot inspect %s: %w", d.Show(name), err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, false, fmt.Errorf("setup: %s is a symbolic link; setup does not follow one, because the file it points at is not the file it was asked to write", d.Show(name))
	case !info.Mode().IsRegular():
		return nil, false, fmt.Errorf("setup: %s exists and is not a regular file", d.Show(name))
	case info.Size() > maxFileBytes:
		return nil, false, fmt.Errorf("setup: %s is larger than %d bytes, which no file setup writes is", d.Show(name), maxFileBytes)
	}
	data, err := d.root.ReadFile(name)
	if err != nil {
		return nil, false, fmt.Errorf("setup: cannot read %s: %w", d.Show(name), err)
	}
	return data, true, nil
}

// CheckWritable proves a new file can be created here, before anything is
// generated or shown. A key displayed to an operator and then not written
// because the directory turned out to be read-only is a key they were told
// to keep for nothing.
func (d *Dir) CheckWritable() error {
	probe := ".pleiades-setup-probe-" + strconv.Itoa(os.Getpid())
	f, err := d.root.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		// A probe a killed run left behind: remove it and try once more.
		_ = d.root.Remove(probe)
		f, err = d.root.OpenFile(probe, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return fmt.Errorf("setup: cannot create a file in %s, so nothing was generated: %w", d.display, err)
	}
	_ = f.Close()
	return d.root.Remove(probe)
}

// CreateNew writes data to name at mode 0600. name must not exist.
//
// The content is written to a temporary name first and then linked into
// place, the same commit internal/crypto.ResolveKey uses for a generated key
// file: a reader never sees name half written, and if something else created
// name in the meantime the link fails rather than replacing it. Where the
// filesystem refuses hard links (some container bind mounts do), it falls
// back to an exclusive create, which still never replaces an existing file.
func (d *Dir) CreateNew(name string, data []byte) error {
	tmp, err := d.writeTemp(name, data)
	if err != nil {
		return err
	}
	defer func() { _ = d.root.Remove(tmp) }()

	err = d.root.Link(tmp, name)
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s was created by something else while setup ran, and setup wrote nothing", ErrChangedUnderneath, d.Show(name))
	}

	f, err := d.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s was created by something else while setup ran, and setup wrote nothing", ErrChangedUnderneath, d.Show(name))
	}
	if err != nil {
		return fmt.Errorf("setup: cannot create %s: %w", d.Show(name), err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("setup: writing %s: %w", d.Show(name), err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("setup: writing %s: %w", d.Show(name), err)
	}
	return f.Close()
}

// Replace writes data over name, but only if name still holds exactly
// expected, the bytes this command read and made its decisions from. That
// check is what stops two setup runs, or a person editing the file, from
// having one of their changes silently undone.
//
// The new content is written to a temporary name and renamed over name, so
// a reader sees the old file or the new one and never a mixture.
func (d *Dir) Replace(name string, expected, data []byte) error {
	current, exists, err := d.ReadExisting(name)
	if err != nil {
		return err
	}
	if !exists || !bytes.Equal(current, expected) {
		return fmt.Errorf("%w: %s changed after setup read it, and setup wrote nothing", ErrChangedUnderneath, d.Show(name))
	}
	tmp, err := d.writeTemp(name, data)
	if err != nil {
		return err
	}
	if err := d.root.Rename(tmp, name); err != nil {
		_ = d.root.Remove(tmp)
		return fmt.Errorf("setup: replacing %s: %w", d.Show(name), err)
	}
	return nil
}

// remove deletes name from the directory. Only the Helm target uses it, to
// take back a file it wrote a moment earlier when its companion could not be
// written.
func (d *Dir) remove(name string) error { return d.root.Remove(name) }

// writeTemp writes data to a new temporary name beside name, at mode 0600,
// synced to disk, and returns the temporary name.
func (d *Dir) writeTemp(name string, data []byte) (string, error) {
	tmp := "." + strings.TrimPrefix(name, ".") + ".setup-" + strconv.Itoa(os.Getpid()) + ".tmp"
	_ = d.root.Remove(tmp)
	f, err := d.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("setup: cannot create a temporary file in %s: %w", d.display, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = d.root.Remove(tmp)
		return "", fmt.Errorf("setup: writing a temporary file in %s: %w", d.display, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = d.root.Remove(tmp)
		return "", fmt.Errorf("setup: writing a temporary file in %s: %w", d.display, err)
	}
	if err := f.Close(); err != nil {
		_ = d.root.Remove(tmp)
		return "", fmt.Errorf("setup: writing a temporary file in %s: %w", d.display, err)
	}
	return tmp, nil
}
