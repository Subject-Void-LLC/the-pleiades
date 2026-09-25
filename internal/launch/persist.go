// Package launch: the launch field that says whether a run keeps SSH
// connections open between a device's tasks.
package launch

// PersistConnectionsField is the job's own step of the connection
// persistence setting: "on" (the default) keeps one SSH connection per
// device open between its tasks, "off" logs in afresh for every task. It
// resolves like any other field, the most specific layer winning. The
// device's own step (engine.PersistConnections) is applied at fan-out,
// and off at either one is off.
const PersistConnectionsField = "persist_connections"

// The two values PersistConnectionsField takes.
const (
	PersistOn  = "on"
	PersistOff = "off"
)

// PersistConnections reports whether f leaves connections persisting: on
// unless the field is set, and then only when it says on. A value that
// is neither is refused where the field is saved, so it can reach here
// only from a row written before that check; it reads as off, since the
// setting exists to be turned off.
func PersistConnections(f Fields) bool {
	raw, set := f[PersistConnectionsField]
	if !set {
		return true
	}
	return raw == PersistOn
}
