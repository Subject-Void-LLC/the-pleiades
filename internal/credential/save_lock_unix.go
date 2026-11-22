//go:build unix

// Package credential: the credential file lock on a Unix system, an flock(2).
package credential

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockFile takes f's exclusive lock without waiting, and returns
// errSaveLockHeld when another open file holds it.
func tryLockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) // #nosec G115 -- an open file's descriptor fits an int
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errSaveLockHeld
	}
	return err
}

// unlockFile releases f's lock.
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN) // #nosec G115 -- an open file's descriptor fits an int
}

// saveLockNoFollow makes the open refuse a symbolic link at the lock file's
// name, so the lock is never a file a link put elsewhere.
const saveLockNoFollow = unix.O_NOFOLLOW
