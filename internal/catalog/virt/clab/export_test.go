// Package clab: test-only access to the reauthentication delay, so a test of
// the one-retry rule does not wait out the real backoff.
package clab

import "time"

// SetReauthDelay sets how long a refused token waits before its fresh login
// and returns a function that puts the old delay back.
func SetReauthDelay(d time.Duration) func() {
	old := reauthDelay
	reauthDelay = d
	return func() { reauthDelay = old }
}
