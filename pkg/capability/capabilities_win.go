package capability

const (
	NameWindows        Name = "WindowsCapable"
	NameWinRM          Name = "WinRMCapable"
	NameWindowsFeature Name = "WindowsFeatureCapable"
)

// WindowsCapable is satisfied by standard Windows devices, parallel to
// LinuxCapable on the identity layer (PLAN.md Section 1b).
type WindowsCapable interface {
	// WindowsEdition returns the detected Windows edition (e.g.
	// "Server 2022 Datacenter").
	WindowsEdition() string
}

// WinRMCapable is satisfied by any device reachable over WinRM, the same
// shape as SSHTransportCapable (host plus port).
type WinRMCapable interface {
	WinRMHost() string
	WinRMPort() int
}

// WindowsFeatureCapable is satisfied by devices that support enabling or
// disabling optional Windows features/roles (e.g. via DISM).
type WindowsFeatureCapable interface {
	// DISMLogPath returns the path DISM writes its log to.
	DISMLogPath() string
}

func init() {
	Register(Descriptor{
		Name:   NameWindows,
		Assert: func(item any) bool { _, ok := item.(WindowsCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameWinRM,
		Assert: func(item any) bool { _, ok := item.(WinRMCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameWindowsFeature,
		Assert: func(item any) bool { _, ok := item.(WindowsFeatureCapable); return ok },
	})
}
