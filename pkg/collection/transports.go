// A Collection method's declared transports, held to the platform's
// vocabulary when it registers and to its device when it runs.
//
// A method declares which transports it supports so that the platform
// can check them before a run, and until Phase 75 nothing did:
// SupportedTransports was printed by `pleiades doc` and read by no check
// at all, so a method that speaks only SSH passed validation against a
// device reachable only over WinRM and failed at run time. It
// was also the reason WindowsShellCapable could not be a child of
// CommandExecCapable: the parent is what exec.command requires, and only
// a transport check keeps an SSH-only method off a Windows server that
// can run commands another way.
package collection

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// checkTransportNames refuses a transport name the platform does not
// know, so a misspelled "shh" fails when the method registers (at process
// start for a built-in, at load for an external program) rather than
// quietly declaring a transport no device can ever reach.
func checkTransportNames(d Descriptor) error {
	for _, t := range d.Manifest.SupportedTransports {
		if _, known := capability.ReachedBy(t); !known {
			return fmt.Errorf("collection: %q declares transport %q, which is not one this platform knows (%s)",
				d.Name, t, strings.Join(capability.Transports(), ", "))
		}
	}
	return nil
}

// CheckTransports refuses a device that the method named fqcn cannot
// reach: m declares one or more transports and device reaches none of
// them. Reaching any one is enough, which is what a method declaring two
// (wait.connection's ssh and winrm) means.
//
// A method that declares no transport has nothing to check: a
// controller-side call such as cloud.aws.* or http.request reaches an
// API, not its device. A nil device has nothing to reach either; whether
// a task may run without one is the execution context's question, not
// this one's.
//
// It is the one check the plan-time rule (internal/validate's
// TransportRule), the run-time gate (internal/engine) and the generic
// dispatchers (pkg.*, svc.*) all call, so the three cannot disagree.
func CheckTransports(device inventory.InventoryItem, fqcn string, m Manifest) error {
	if len(m.SupportedTransports) == 0 || device == nil {
		return nil
	}
	for _, t := range m.SupportedTransports {
		if reaches(device, t) {
			return nil
		}
	}
	return fmt.Errorf("collection method %q reaches its device over %s, and device %q reaches %s",
		fqcn, strings.Join(m.SupportedTransports, " or "), device.Name(), describeReach(device))
}

// reaches reports whether device has any capability that reaches
// transport. An unknown transport is reached by nothing.
func reaches(device inventory.InventoryItem, transport string) bool {
	names, _ := capability.ReachedBy(transport)
	for _, name := range names {
		if device.HasCapability(name) {
			return true
		}
	}
	return false
}

// describeReach names the transports device does reach, for a refusal
// that tells an operator what the device can do instead of only what it
// cannot.
func describeReach(device inventory.InventoryItem) string {
	var reached []string
	for _, t := range capability.Transports() {
		if reaches(device, t) {
			reached = append(reached, t)
		}
	}
	if len(reached) == 0 {
		return "none of the transports this platform knows"
	}
	return "only " + strings.Join(reached, " and ")
}
