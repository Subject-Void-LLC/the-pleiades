// Devices many runs may work on at once.
package inventory

// Shared is implemented by a device many runs may work on at the same time
// because its own API settles concurrent writers: a Kubernetes cluster,
// whose objects carry resource versions and whose server applies
// server-side apply's field ownership. Its tasks take a shared lock rather
// than the exclusive one every other device's take (Part XVII D3), so runs
// against one cluster do not queue behind each other.
type Shared interface {
	// SharedAcrossRuns reports whether this device is shared.
	SharedAcrossRuns() bool
}

// IsShared reports whether device is a Shared device that says it is.
func IsShared(device InventoryItem) bool {
	s, ok := device.(Shared)
	return ok && s.SharedAcrossRuns()
}
