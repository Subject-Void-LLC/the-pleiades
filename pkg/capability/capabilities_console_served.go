// Package capability: a device whose CLI is reached through a console
// server rather than at an address of its own, such as a node of a Cisco
// Modeling Labs lab reached through its controller's console server.
package capability

// NameConsoleServed is the capability of a device reached through a console
// server.
const NameConsoleServed Name = "ConsoleServedCapable"

// ConsoleServedCapable is satisfied by a device whose CLI is reached
// through a console server: another inventory device, logged in to with
// that device's own credential and verified host key, then attached to this
// device's console line, where this device's own login is answered. It
// needs no address of its own and no route to it.
type ConsoleServedCapable interface {
	// ConsoleServer returns the inventory name of the device whose console
	// server reaches this one (a cml_controller).
	ConsoleServer() string
	// ConsoleLine returns this device's console on that server: for CML,
	// the console's UUID, which names exactly one node's line.
	ConsoleLine() string
}

func init() {
	Register(Descriptor{
		Name:   NameConsoleServed,
		Assert: func(item any) bool { _, ok := item.(ConsoleServedCapable); return ok },
	})
}
