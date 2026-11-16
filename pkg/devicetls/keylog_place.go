// Package devicetls: where a run's TLS key log may be made. Its directory
// must exist and be a directory, it may not be, or lie below, any directory
// the caller keeps it out of (the project, whose files a run's methods read
// and send to devices, or a directory a third-party program reads), and the
// name may not exist. A directory is compared by identity (os.SameFile), not
// by name, so neither a link, a name differing only in case on a
// case-insensitive filesystem, nor a bind mount carries the key log in. The
// directory is held open from its check to the file's creation, so a path
// swapped for a link in between cannot send the file elsewhere.
//
// Windows is refused: it ignores a file's mode, so a new file takes its
// directory's access list, and the promise that the key log is readable by
// its owner alone would not hold there.
package devicetls

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// KeepOut is a directory a key log may not be made in or below, with what it
// is and why, for the refusal to say.
type KeepOut struct {
	// Dir is the directory.
	Dir string
	// What names it, such as "the project".
	What string
	// Why says what would read a key log there.
	Why string
}

// afterKeyLogCheck, when set, runs between a key log directory's check and
// the file's creation. Only a test sets it, to change the path in between.
var afterKeyLogCheck func()

// unsupportedOn says why a key log cannot be made on goos, or nil where it
// can.
func unsupportedOn(goos string) error {
	if goos == "windows" {
		return errors.New("a TLS key log cannot be written on Windows yet: Windows ignores the file's mode, so the key log would take its directory's access list rather than being readable by its owner alone")
	}
	return nil
}

// CheckKeyLogPath refuses, before a run starts, what CreateKeyLog would
// refuse for path: Windows, an empty name or "-", a directory that does not
// exist or is not one, a directory in or below one of keepOut, and a name
// that exists, a link included. CreateKeyLog checks everything again as it
// creates the file, so passing here is not a promise.
func CheckKeyLogPath(path string, keepOut ...KeepOut) error {
	d, err := openKeyLogDir(path, keepOut)
	if err != nil {
		return err
	}
	defer d.close()
	if _, err := d.root.Lstat(d.base); err == nil {
		return existsError(d.abs)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checking the TLS key log %s: %w", d.abs, err)
	}
	return nil
}

// existsError is the refusal of a key log name that exists.
func existsError(abs string) error {
	return fmt.Errorf("the TLS key log %s already exists: a key log is never written over or added to, so name a file that does not exist yet", abs)
}

// keyLogDir is the directory a key log is made in, held open from its check
// to the file's creation.
type keyLogDir struct {
	// abs is the key log's absolute path, which may be shown.
	abs string
	// base is its name in root.
	base string
	// root is the directory, held open.
	root *os.Root
}

// close releases the held directory.
func (d *keyLogDir) close() { _ = d.root.Close() }

// openKeyLogDir opens path's directory and checks it, refusing everything
// CheckKeyLogPath lists but an existing name.
func openKeyLogDir(path string, keepOut []KeepOut) (*keyLogDir, error) {
	if err := unsupportedOn(runtime.GOOS); err != nil {
		return nil, err
	}
	switch path {
	case "":
		return nil, errors.New("the TLS key log needs a file name")
	case "-":
		return nil, errors.New("the TLS key log is written to a new file, never to standard output: name a file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving the TLS key log %s: %w", path, err)
	}
	parent := filepath.Dir(abs)
	info, err := os.Stat(parent)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("the directory %s, where the TLS key log %s would go, does not exist: make it first", parent, abs)
	case err != nil:
		return nil, fmt.Errorf("checking the TLS key log's directory %s: %w", parent, err)
	case !info.IsDir():
		return nil, fmt.Errorf("%s, where the TLS key log %s would go, is not a directory", parent, abs)
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, fmt.Errorf("opening the TLS key log's directory %s: %w", parent, err)
	}
	d := &keyLogDir{abs: abs, base: filepath.Base(abs), root: root}
	if err := d.keptOut(parent, keepOut); err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

// keptOut refuses the held directory when it is, or lies below, one of
// keepOut, comparing identities. It walks up the directory's path with its
// links resolved, which is the chain of directories the held one really sits
// in, and first holds that path to the held directory itself, so the chain
// checked is the held directory's own.
func (d *keyLogDir) keptOut(parent string, keepOut []KeepOut) error {
	held, err := d.root.Stat(".")
	if err != nil {
		return fmt.Errorf("checking the TLS key log's directory %s: %w", parent, err)
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolving the TLS key log's directory %s: %w", parent, err)
	}
	if at, err := os.Stat(resolved); err != nil || !os.SameFile(at, held) {
		return fmt.Errorf("the directory %s changed while the TLS key log was being checked: run again", parent)
	}
	type guard struct {
		keep KeepOut
		info fs.FileInfo
	}
	var guards []guard
	for _, k := range keepOut {
		info, err := os.Stat(k.Dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Nothing is there for the key log to be inside.
			continue
		case err != nil:
			return fmt.Errorf("checking %s %s: %w", k.What, k.Dir, err)
		}
		guards = append(guards, guard{keep: k, info: info})
	}
	for cur := resolved; ; cur = filepath.Dir(cur) {
		info, err := os.Stat(cur)
		if err != nil {
			return fmt.Errorf("checking the TLS key log's directory %s: %w", cur, err)
		}
		for _, g := range guards {
			if os.SameFile(info, g.info) {
				return fmt.Errorf("the TLS key log %s would be inside %s %s, %s: write it outside", d.abs, g.keep.What, g.keep.Dir, g.keep.Why)
			}
		}
		if filepath.Dir(cur) == cur {
			return nil
		}
	}
}

// CreateKeyLog creates path as a new key log, mode 0600, refusing everything
// CheckKeyLogPath refuses. The name is created with O_EXCL in the directory
// checked, held open from the check, so a name that exists (a file, a
// directory, or a link, dangling or not) is refused rather than written over
// or added to, and a path changed after the check cannot send the file
// somewhere the check did not see.
func CreateKeyLog(path string, keepOut ...KeepOut) (*KeyLog, error) {
	d, err := openKeyLogDir(path, keepOut)
	if err != nil {
		return nil, err
	}
	defer d.close()
	if afterKeyLogCheck != nil {
		afterKeyLogCheck()
	}
	f, err := d.root.OpenFile(d.base, os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyLogMode)
	switch {
	case errors.Is(err, fs.ErrExist):
		return nil, existsError(d.abs)
	case err != nil:
		return nil, fmt.Errorf("creating the TLS key log %s: %w", d.abs, err)
	}
	return &KeyLog{path: d.abs, f: f}, nil
}
