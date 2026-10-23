// A run's project files, written by a method through sdk.ProjectFileWriter
// on the machine that holds the project: the command line.
package engine

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// projectDirMode is the mode of a directory a write creates in the
// project: the owner's alone, like the files a method writes by default.
const projectDirMode = 0o700

// WriteProjectFile implements sdk.ProjectFileWriter. name is held to
// ReadProjectFile's rules (projectRelative, then no symbolic link on any
// component, checked through an os.Root on the project directory so
// nothing it names escapes it). A missing parent directory is created
// inside the project; an existing component that is not a directory, or a
// target that is not a regular file, is refused. The write goes to a
// temporary file beside the target, which is renamed over it, so the
// target is never seen half written.
func (c *runbookContext) WriteProjectFile(name string, data []byte, perm fs.FileMode, write bool) (bool, error) {
	if c.projectDir == "" {
		return false, sdk.ErrNoProjectFiles
	}
	if len(data) > sdk.MaxProjectFileWrite {
		return false, fmt.Errorf("%s: %d bytes is over the %d one write takes", name, len(data), sdk.MaxProjectFileWrite)
	}
	if perm&^fs.ModePerm != 0 {
		return false, fmt.Errorf("%s: mode %#o holds more than permission bits", name, perm)
	}
	clean, err := projectRelative(name)
	if err != nil {
		return false, err
	}
	root, err := os.OpenRoot(c.projectDir)
	if err != nil {
		return false, fmt.Errorf("opening the project directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	parts := strings.Split(clean, "/")
	// Every parent: a directory, never a link; one that is missing is
	// created (by a real write) and everything under it is missing too.
	missing := false
	for i := range parts[:len(parts)-1] {
		dir := strings.Join(parts[:i+1], "/")
		if missing {
			break
		}
		info, err := root.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			missing = true
		case err != nil:
			return false, fmt.Errorf("%s: %w", name, err)
		case info.Mode()&os.ModeSymlink != 0:
			return false, fmt.Errorf("%s passes through a symbolic link (%s); name the file itself", name, dir)
		case !info.IsDir():
			return false, fmt.Errorf("%s: %s is not a directory", name, dir)
		}
	}
	if missing {
		if !write {
			return true, nil
		}
		if err := root.MkdirAll(path.Dir(clean), projectDirMode); err != nil {
			return false, fmt.Errorf("%s: creating its directory: %w", name, err)
		}
		return true, writeAtomically(root, clean, data, perm)
	}

	same, err := projectFileHolds(root, name, clean, data, perm)
	if err != nil {
		return false, err
	}
	if same {
		return false, nil
	}
	if !write {
		return true, nil
	}
	return true, writeAtomically(root, clean, data, perm)
}

// projectFileHolds reports whether the file clean names already holds data
// with mode perm. A file that does not exist holds nothing; a link or
// anything but a regular file is refused, naming name, the path as given.
func projectFileHolds(root *os.Root, name, clean string, data []byte, perm fs.FileMode) (bool, error) {
	info, err := root.Lstat(clean)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("%s is a symbolic link; name the file itself", name)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Mode().Perm() != perm || info.Size() != int64(len(data)) {
		return false, nil
	}
	f, err := root.Open(clean)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	held, err := io.ReadAll(io.LimitReader(f, int64(len(data))+1))
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return bytes.Equal(held, data), nil
}

// writeAtomically writes data to a new temporary file in clean's
// directory, with mode perm set on it before any byte is written (the
// umask does not decide it), flushes it, and renames it over clean. On any
// failure the temporary file is removed and clean is untouched.
func writeAtomically(root *os.Root, clean string, data []byte, perm fs.FileMode) error {
	var suffix [8]byte
	_, _ = rand.Read(suffix[:]) // crypto/rand's Read never fails
	tmp := path.Join(path.Dir(clean), ".pleiades-write-"+hex.EncodeToString(suffix[:]))
	f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("%s: %w", clean, err)
	}
	if err = fill(f, data, perm); err == nil {
		err = root.Rename(tmp, clean)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return fmt.Errorf("%s: %w", clean, err)
	}
	return nil
}

// fill sets f's mode, writes data, flushes and closes it, joining whatever
// failed.
func fill(f *os.File, data []byte, perm fs.FileMode) error {
	err := f.Chmod(perm)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	return errors.Join(err, f.Close())
}
