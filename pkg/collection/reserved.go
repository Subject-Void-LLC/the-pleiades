// Parameter names the engine reads from a task's params itself, and which a
// Collection method therefore may not declare as its own.
//
// A task's params reach two readers. The engine reads a few keys to decide
// how to run the task, and the method reads the rest to decide what to do.
// Both see the same map, so a method that declares one of the engine's keys
// gives a single value two meanings at once. That happened: net.netconf.config
// named its datastore parameter target, so `target: candidate` asked the
// method for the candidate datastore and asked the engine for a device or tag
// named candidate. Whoever could name a device or a tag that way received the
// configuration push.
//
// A rollback makes the collision worse rather than just older: an undo is
// replayed with params recorded from the forward run, so a recorded key the
// engine also reads would move the undo to a different device.
package collection

import (
	"fmt"
	"slices"
)

// TargetParam is the task param the engine reads as the device name or
// inventory tag a task runs against (internal/engine.TaskTarget). Every
// task may carry it, whatever method it calls, and no method may declare
// it.
const TargetParam = "target"

// reservedParams lists every param the engine reads for itself.
var reservedParams = []string{TargetParam}

// ReservedParams returns the param names the engine reads for itself, in a
// fresh slice a caller may keep.
func ReservedParams() []string {
	return slices.Clone(reservedParams)
}

// IsReservedParam reports whether name is a param the engine reads for
// itself, so that no method may declare it.
func IsReservedParam(name string) bool {
	return slices.Contains(reservedParams, name)
}

// checkReservedParams refuses a method whose documentation declares a param
// the engine reads for itself.
//
// It reads Doc.Params because that is where a method says which keys it
// reads. A method that reads a reserved key without declaring it is not
// caught here, and ParamsRule (internal/validate) does not catch it either,
// since it allows every reserved key on every task. That gap is the one
// every undocumented parameter has; nothing here can see inside Invoke.
func checkReservedParams(d Descriptor) error {
	for _, p := range d.Manifest.Doc.Params {
		if IsReservedParam(p.Name) {
			return fmt.Errorf("collection: %q declares a parameter named %q, which the engine reads as "+
				"the device or tag a task runs on; give the method's own parameter another name", d.Name, p.Name)
		}
	}
	return nil
}
