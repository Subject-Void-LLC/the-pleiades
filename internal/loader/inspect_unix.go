//go:build unix

// Package loader: the ownership, permission and digest checks applied to
// the directory and to each program, at load time and again before every
// run.
package loader

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// writableByOthers is the group-write and world-write permission bits.
const writableByOthers fs.FileMode = 0o022

// ownerExecute is the owner-execute permission bit.
const ownerExecute fs.FileMode = 0o100

// checkDir resolves dir to an absolute path with every symlink in it
// resolved, and refuses it unless it is a directory that only its owner
// can write, owned by this process's user or root.
//
// Resolving first means every later check, and every path a program is
// run from, names the real directory rather than a link to it that could
// be pointed somewhere else between two runs.
func checkDir(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no directory given")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("directory %s: %w", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("directory %s: %w", dir, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("directory %s: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", resolved)
	}
	if err := checkOwnership(info); err != nil {
		return "", fmt.Errorf("directory %s %w", resolved, err)
	}
	return resolved, nil
}

// checkOwnership refuses anything group- or world-writable, or owned by
// anyone other than this process's effective user or root. Its errors
// read as the end of a sentence naming the path.
//
// Either one would let a second account decide what Pleiades runs:
// writable by others directly, and owned by someone else because an owner
// can always change the mode back.
func checkOwnership(info fs.FileInfo) error {
	if perm := info.Mode().Perm(); perm&writableByOthers != 0 {
		return fmt.Errorf("is group- or world-writable (mode %04o): only its owner may be able to change what Pleiades runs", perm)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("has no owner this platform can report, so its ownership cannot be checked")
	}
	euid := os.Geteuid()
	if st.Uid != 0 && int64(st.Uid) != int64(euid) {
		return fmt.Errorf("is owned by uid %d, which is neither this process's user (uid %d) nor root", st.Uid, euid)
	}
	return nil
}

// inspectProgram checks that path is a program Load may run and returns
// the digest of its bytes (openProgram), closing the file again.
func inspectProgram(path string) (string, error) {
	f, digest, err := openProgram(path)
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return digest, nil
}

// openProgram checks that path is a program Load may run, and returns it
// open, with the digest of its bytes as "sha256:" and lowercase hex.
// Every error it returns reads as the end of a sentence naming the path
// ("is a symlink", "could not be opened: ..."), so a caller can prefix
// the path alone.
//
// It is the one check used both at load time and immediately before every
// run, so the two cannot drift apart. The digest is computed through the
// same open file descriptor the permissions were read from (fstat), so
// the bytes hashed and the mode checked belong to one file even if the
// name is swapped underneath. O_NOFOLLOW refuses a symlink at open time
// even if one appeared after the Lstat below, and O_NONBLOCK keeps a FIFO
// swapped in at that moment from blocking the open forever.
//
// The caller runs the program through this same open file (newCommand
// executes /proc/self/fd/<n>, never the path), so a program renamed or
// replaced under its name after this check is not what runs: the file
// checked, hashed and approved is the file executed. What remains is the
// file's owner rewriting it in place, which the ownership checks already
// confine to that owner or root. The caller closes it.
func openProgram(path string) (*os.File, string, error) {
	linfo, err := os.Lstat(path)
	if err != nil {
		return nil, "", fmt.Errorf("could not be inspected: %w", err)
	}
	if linfo.Mode()&fs.ModeSymlink != 0 {
		return nil, "", errors.New("is a symlink; the directory must hold the programs themselves, since a link can point anywhere, including somewhere other people can write")
	}
	if !linfo.Mode().IsRegular() {
		return nil, "", fmt.Errorf("is not a regular file (%s); the directory holds programs only", describeFileType(linfo.Mode()))
	}

	// #nosec G304 -- path is an entry of the directory checkDir already
	// vetted (owned by this user or root, writable by no one else), and
	// O_NOFOLLOW refuses anything but the file itself.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, "", fmt.Errorf("could not be opened: %w", err)
	}
	fail := func(err error) (*os.File, string, error) {
		_ = f.Close()
		return nil, "", err
	}

	info, err := f.Stat()
	if err != nil {
		return fail(fmt.Errorf("could not be inspected: %w", err))
	}
	mode := info.Mode()
	if !mode.IsRegular() {
		return fail(fmt.Errorf("is not a regular file (%s); the directory holds programs only", describeFileType(mode)))
	}
	if mode&ownerExecute == 0 {
		// Refused rather than skipped: this directory holds programs and
		// nothing else, so a file without the bit is almost always a
		// forgotten chmod, and that should be heard rather than silently
		// leave a Collection unloaded.
		return fail(fmt.Errorf("is not executable by its owner (mode %04o)", mode.Perm()))
	}
	if mode&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
		return fail(errors.New("is setuid or setgid; an external Collection runs as the Pleiades user and never needs to change who it runs as"))
	}
	if err := checkOwnership(info); err != nil {
		return fail(err)
	}

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fail(fmt.Errorf("could not be read to compute its digest: %w", err))
	}
	return f, "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// describeFileType names what kind of non-regular file mode describes,
// for a refusal message.
func describeFileType(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeDevice != 0:
		return "a device"
	case mode&fs.ModeSymlink != 0:
		return "a symlink"
	default:
		return "type " + mode.Type().String()
	}
}
