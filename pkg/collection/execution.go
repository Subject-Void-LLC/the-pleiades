// Where a Collection method's code runs, and whether it acts on a device:
// the execution context, as data a runbook's plan and its executor read.
//
// There are three contexts: target-side (on or against the device),
// controller-side (in the host process: an API call, a notification) and
// hybrid. One more thing decides how a task runs: whether
// the method acts on a device at all. A controller-side method may still
// need one (cloud.aws.* acts on an account device, net.catalyst.* on a
// network controller's API), may need one only for some
// calls (http.request with a path on a device's API, and not with a full
// URL), or may need none. A call that needs no device does not inherit its
// runbook's hosts: and runs once with no device and no credential; a call
// that needs one keeps today's behavior. That second half is what keeps an
// existing http.request path call under hosts: unchanged.
package collection

import "fmt"

// Site is where a Collection method's code runs.
type Site string

const (
	// SiteTarget runs on or against the device: a command over SSH or
	// WinRM, a NETCONF edit, a package install.
	SiteTarget Site = "target"
	// SiteController runs in the host process, the CLI or a Runner, with
	// no connection to a device's shell: an API call, a local program.
	SiteController Site = "controller"
	// SiteHybrid runs a controller half in the host process and a target
	// half on the device.
	SiteHybrid Site = "hybrid"
)

// DeviceUse says whether a Collection method acts on a device.
type DeviceUse string

const (
	// DeviceRequired acts on its target device on every call.
	DeviceRequired DeviceUse = "required"
	// DeviceOptional acts on a device for some calls and not others, and
	// answers which through Descriptor.DeviceCall.
	DeviceOptional DeviceUse = "optional"
	// DeviceNone never acts on a device.
	DeviceNone DeviceUse = "none"
)

// NeedsDevice reports whether a call to d with params acts on a device,
// and so whether its task inherits its runbook's hosts: when it names no
// target of its own. A method that does not say (an external program's,
// or one from before the field existed) needs one, which is what every
// method did before.
func (d Descriptor) NeedsDevice(params map[string]any) bool {
	switch d.Manifest.ExecutionContext.Device {
	case DeviceNone:
		return false
	case DeviceOptional:
		return d.DeviceCall == nil || d.DeviceCall(params)
	default:
		return true
	}
}

// checkExecutionContext refuses an execution context that is not one of
// the declared values or contradicts itself: a target-side method that
// claims to need no device, an optional device with no DeviceCall to
// decide it, or a DeviceCall on a method whose device is not optional.
// An external program's method cannot carry a DeviceCall (a description
// carries only data), so it cannot declare an optional device either.
func checkExecutionContext(d Descriptor) error {
	ec := d.Manifest.ExecutionContext
	switch ec.Site {
	case "", SiteTarget, SiteController, SiteHybrid:
	default:
		return fmt.Errorf("collection: %q declares execution site %q, which is not %s, %s or %s", d.Name, ec.Site, SiteTarget, SiteController, SiteHybrid)
	}
	switch ec.Device {
	case "", DeviceRequired, DeviceOptional, DeviceNone:
	default:
		return fmt.Errorf("collection: %q declares device use %q, which is not %s, %s or %s", d.Name, ec.Device, DeviceRequired, DeviceOptional, DeviceNone)
	}
	if (ec.Site == "" || ec.Site == SiteTarget) && (ec.Device == DeviceOptional || ec.Device == DeviceNone) {
		return fmt.Errorf("collection: %q runs on its target and declares device use %q; a target-side method acts on a device on every call", d.Name, ec.Device)
	}
	if ec.Device == DeviceOptional && d.DeviceCall == nil {
		return fmt.Errorf("collection: %q declares an optional device and no DeviceCall to say which calls need one", d.Name)
	}
	if d.DeviceCall != nil && ec.Device != DeviceOptional {
		return fmt.Errorf("collection: %q carries a DeviceCall but declares device use %q; only an optional device is decided per call", d.Name, ec.Device)
	}
	return nil
}
