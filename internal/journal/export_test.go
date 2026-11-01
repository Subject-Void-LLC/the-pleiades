// Package journal: the unexported seams its external tests reach, and
// nothing else.
package journal

import (
	"path/filepath"
	"time"
)

// AllForJobWithin and OnDevicesSinceWithin are the rollback reads with
// their bounds as parameters, so a test can reach them without writing a
// hundred thousand rows.
var (
	AllForJobWithin      = (*EntStore).allForJob
	OnDevicesSinceWithin = (*EntStore).onDevicesSince
)

// HoldClockLock takes the run counter's lock of the project at root, as a
// tick holds it, so a test can see another tick wait for it.
func HoldClockLock(root string) (func() error, error) {
	return lockClock(filepath.Join(root, pleiadesDirName))
}

// SetClockLockWait shortens how long a tick waits for the counter's lock, for
// the rest of the test.
func SetClockLockWait(t interface{ Cleanup(func()) }, d time.Duration) {
	old := clockLockWait
	clockLockWait = d
	t.Cleanup(func() { clockLockWait = old })
}
