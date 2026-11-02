// Package facts: what the external tests read of the file store's internals.
package facts

import "os"

// LockOpenRetries is how often openLock has tried again in this process.
func LockOpenRetries() int64 { return lockOpenRetries.Load() }

// OpenLock is openLock, for a test of a directory removed under it.
func OpenLock(r *os.Root, name string) (*os.File, error) { return openLock(r, name) }
