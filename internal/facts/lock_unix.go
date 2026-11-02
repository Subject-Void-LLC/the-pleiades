//go:build unix

// Package facts: a device's fact files are written under an flock(2), so two
// runs of the CLI at once take turns rather than interleave.
package facts

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes f's exclusive lock, waiting for it.
func lockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX) // #nosec G115 -- an open file's descriptor fits an int
}

// unlockFile releases f's lock.
func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN) // #nosec G115 -- an open file's descriptor fits an int
}
