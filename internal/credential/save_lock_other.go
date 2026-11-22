//go:build !unix && !windows

// Package credential: no credential file lock where the platform offers
// neither flock nor LockFileEx. The CLI is built for Linux, macOS, FreeBSD
// and Windows; this keeps the package compiling anywhere else, and says so
// rather than pretending: writers there are not kept apart.
package credential

import "os"

// tryLockFile takes no lock on this platform.
func tryLockFile(*os.File) error { return nil }

// unlockFile releases nothing on this platform.
func unlockFile(*os.File) error { return nil }

// saveLockNoFollow is nothing on this platform.
const saveLockNoFollow = 0
