// Package gate: test seams, compiled only into this package's tests.
package gate

import (
	"testing"
	"time"
)

// SetProbeTimeout shortens a precondition probe's budget for one test, so a
// TCP precondition against an address that drops packets (TEST-NET-1)
// skips in seconds rather than the production 30. No test in this package
// runs in parallel, so nothing reads it while it changes.
func SetProbeTimeout(t testing.TB, d time.Duration) {
	t.Helper()
	old := probeTimeout
	probeTimeout = d
	t.Cleanup(func() { probeTimeout = old })
}
