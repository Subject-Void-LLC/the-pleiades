// Whether this process may reach a device's engine through its own sockets.
package containerengine

import (
	"fmt"
	"sync/atomic"
)

// localCarrier says whether this process may use capability.CarrierLocal.
// Off unless a composition root says otherwise; see AllowLocalCarrier.
var localCarrier atomic.Bool

// AllowLocalCarrier lets this process reach a device's engine through the
// process's own sockets (capability.CarrierLocal, the engine_local
// transport). Only a composition root whose process is the machine whose
// engine onboarding found calls it: the CLI, which runs every task in its
// own process. A Runner never does. Its per-task child is a fresh process
// with no composition root, so the refusal is the default rather than
// something each Runner must remember; and a Runner's own machine is never a
// device: a record claiming local reach there names whichever Runner took
// the job, which the identity pin cannot always tell apart (two Runners
// built from one image share Podman's hostname-and-paths identity).
// Onboarding grants the capability on the same terms
// (onboard.AllowLocalEngine).
func AllowLocalCarrier() { localCarrier.Store(true) }

// refuseLocalCarrier is the error for a local carrier this process may not
// use.
func refuseLocalCarrier(fqcn, device string) error {
	return fmt.Errorf("%s: device %s is reached through this machine's own engine socket (engine_local), which only the pleiades command line may use: a Runner's machine is not the device; run the task with `pleiades run`, or onboard the device over SSH or TLS", fqcn, device)
}
