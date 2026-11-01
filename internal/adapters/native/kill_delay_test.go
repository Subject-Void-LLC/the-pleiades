// Tests that a collection child running a detached command is given the
// time its stop on the device takes before it is killed.
package native

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestKillDelay_ADetachedChildWaitsOutTheStop: the kill after SIGTERM comes
// childWaitDelay after it for a plain call, and only after the detached
// command's whole stop budget for a detached one.
func TestKillDelay_ADetachedChildWaitsOutTheStop(t *testing.T) {
	if got := killDelay(false); got != childWaitDelay {
		t.Errorf("killDelay(plain) = %s; want %s", got, childWaitDelay)
	}
	if got := killDelay(true); got <= remoteexec.DetachStopBudget {
		t.Errorf("killDelay(detached) = %s; want more than the stop budget, %s", got, remoteexec.DetachStopBudget)
	}
}
