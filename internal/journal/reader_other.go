//go:build !unix

// Package journal: the reader's file checks where Unix modes and owners do
// not apply (Windows). It still refuses a symbolic link, anything but a
// regular file, and an oversized file; access is the directory's ACL's to
// decide.
package journal

import (
	"fmt"
	"os"
)

// openPrivate opens path for reading if it is a regular file, not a
// symbolic link, no larger than limit. The check and the open are two
// steps here, so a link swapped in between them is not caught, where the
// Unix half's O_NOFOLLOW closes that window; the journal directory's own
// ACL is what keeps another user from making the swap.
func openPrivate(path string, limit int64) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes, more than any run writes", path, info.Size())
	}
	// #nosec G304 -- path is the project's own journal directory joined
	// with a name fileNameFor restricted.
	return os.Open(path)
}

// othersMayWrite reports false: Windows has no group or world bits, and Go
// reports every writable file as 0666, so the Unix test would skip every
// active-run record and never clean one up. Who may write the record is the
// record directory's ACL's to decide, as it is for the journal itself.
func othersMayWrite(os.FileInfo) bool {
	return false
}

// checkJournalDir refuses a journal directory that is a link or not a
// directory.
func checkJournalDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("the journal directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the journal directory %s is not a directory", dir)
	}
	return nil
}
