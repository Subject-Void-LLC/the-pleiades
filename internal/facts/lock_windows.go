//go:build windows

// Package facts: a device's fact files are written under a LockFileEx
// byte-range lock, so two runs of the CLI at once take turns.
package facts

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes f's exclusive lock, waiting for it.
func lockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &o)
}

// unlockFile releases f's lock.
func unlockFile(f *os.File) error {
	var o windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &o)
}
