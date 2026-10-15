// Test seams for the docker package's own tests, compiled only into them.
package docker

import "time"

// SetCallTimeoutsForTest sets every docker call's deadline to d, so a test
// can watch one end without waiting a real one out, and returns the
// function that restores them.
func SetCallTimeoutsForTest(d time.Duration) (restore func()) {
	quick, start, stop, remove := quickCallTimeout, startCallTimeout, stopCallTimeout, removeCallTimeout
	quickCallTimeout, startCallTimeout, stopCallTimeout, removeCallTimeout = d, d, d, d
	return func() {
		quickCallTimeout, startCallTimeout, stopCallTimeout, removeCallTimeout = quick, start, stop, remove
	}
}
