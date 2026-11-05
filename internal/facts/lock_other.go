//go:build !unix && !windows

// Package facts: no file lock where the platform offers neither flock nor
// LockFileEx; the CLI is built for Linux, macOS and Windows, and this keeps
// the package compiling anywhere else, saying so rather than pretending.
package facts

import "os"

// lockFile takes no lock on this platform.
func lockFile(*os.File) error { return nil }

// unlockFile releases nothing on this platform.
func unlockFile(*os.File) error { return nil }

// syncDir does nothing where the platform is neither Unix nor Windows.
func syncDir(*os.Root) {}
