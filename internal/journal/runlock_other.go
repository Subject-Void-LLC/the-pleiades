//go:build !unix && !windows

// Package journal: no run lock where the platform offers neither flock nor
// LockFileEx. The CLI is built for Linux, macOS and Windows; this keeps
// the package compiling anywhere else, and says so rather than pretending.
package journal

import "os"

// lockFile takes no lock on this platform.
func lockFile(*os.File, bool) error { return nil }

// unlockFile releases nothing on this platform.
func unlockFile(*os.File) error { return nil }
