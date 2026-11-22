//go:build windows

// Package credential: the credential file lock on Windows, a LockFileEx
// byte-range lock.
package credential

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes f's exclusive lock without waiting, and returns
// errSaveLockHeld when another handle holds it.
func tryLockFile(f *os.File) error {
	var o windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errSaveLockHeld
	}
	return err
}

// unlockFile releases f's lock.
func unlockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
}

// saveLockNoFollow is nothing on Windows, whose open has no such flag; the
// project directory's ACL is what keeps another user from putting a link
// there.
const saveLockNoFollow = 0
