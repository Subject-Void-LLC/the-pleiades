//go:build windows

// Package journal: the run lock on Windows, a LockFileEx byte-range lock.
package journal

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes f's lock, shared or exclusive, without waiting.
func lockFile(f *os.File, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	var o windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &o)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLockHeld
	}
	return err
}

// unlockFile releases f's lock.
func unlockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
}
