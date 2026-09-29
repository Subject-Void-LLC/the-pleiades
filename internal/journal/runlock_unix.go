//go:build unix

// Package journal: the run lock on a Unix system, an flock(2).
package journal

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes f's lock, shared or exclusive, without waiting.
func lockFile(f *os.File, exclusive bool) error {
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	err := unix.Flock(int(f.Fd()), how|unix.LOCK_NB) // #nosec G115 -- an open file's descriptor fits an int
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errLockHeld
	}
	return err
}

// unlockFile releases f's lock.
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN) // #nosec G115 -- an open file's descriptor fits an int
}
