//go:build unix

// Package journal: the reader's file checks on a Unix system.
package journal

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openPrivate opens path for reading only if it is a regular file, not a
// symbolic link, owned by this process's user, readable and writable by
// no one else, and no larger than limit: the file the writer creates
// (fileMode, 0600) and nothing an attacker could put in its place.
func openPrivate(path string, limit int64) (*os.File, error) {
	// O_NOFOLLOW refuses a symbolic link at the last component, so the file
	// checked below is the file read, not one a link swapped in.
	// #nosec G304 -- path is the project's own journal directory joined
	// with a name fileNameFor restricted.
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := checkPrivate(info, path); err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > limit {
		_ = f.Close()
		return nil, fmt.Errorf("%s is %d bytes, more than any run writes", path, info.Size())
	}
	return f, nil
}

// checkJournalDir refuses a journal directory that is a symbolic link, is
// not this user's, or others may write.
func checkJournalDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("the journal directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the journal directory %s is not a directory", dir)
	}
	return checkPrivate(info, dir)
}

// checkPrivate refuses a file or directory another user owns, or that
// group or others may read or write.
func checkPrivate(info os.FileInfo, path string) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s has mode %04o; the journal is kept owner-only, so it was not written by a run", path, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s belongs to another user", path)
	}
	return nil
}
