// Exposes the poll interval to this package's external tests, which need
// no five-second waits against an in-memory controller.
package lab

import "time"

// SetPollInterval sets how often a wait reads node states.
func SetPollInterval(d time.Duration) { pollInterval = d }
