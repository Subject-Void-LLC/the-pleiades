package capability

const (
	NameWindows        Name = "WindowsCapable"
	NameWinRM          Name = "WinRMCapable"
	NameWindowsFeature Name = "WindowsFeatureCapable"
	NameWindowsShell   Name = "WindowsShellCapable"
)

// WindowsCapable is satisfied by standard Windows devices, parallel to
// LinuxCapable on the identity layer.
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

// WindowsShellCapable is satisfied by a Windows device that can run a
// command three ways over its transport: directly, through cmd.exe, or
// through PowerShell. It names both interpreters, because a Windows host
// has two shells with disjoint metacharacter sets, and a single shell
// path (ShellExecCapable's contract, which keeps its POSIX meaning) could
// not say which parser is on the far end. A device that ships its
// interpreters somewhere other than the stock paths says so here rather
// than in Go.
//
// Its parent is CommandExecCapable, because a Windows shell device can
// also run a command outside any shell: WinRM's direct mode (ShellNone)
// is exactly CommandExecCapable's "an arbitrary command outside of a
// shell". Declaring this declares that (Resolves). Phase 75 had to
// register it with no parent at first: CommandExecCapable is what
// exec.command requires, exec.command speaks SSH only, and nothing then
// checked a method's transports against a device, so a Windows server
// would have passed validation for it and failed at run time. The
// transport check (collection.CheckTransports, at plan time and again
// before a method runs) is what refuses that now, so the capability says
// what the device can do and the transport says how it is reached.
type WindowsShellCapable interface {
	// CommandExecCapable's WorkingDirectory returns where a command
	// starts, or "" to leave it to the service.
	CommandExecCapable
	// CmdPath returns the absolute path to cmd.exe on the device.
	CmdPath() string
	// PowerShellPath returns the absolute path to powershell.exe on the
	// device.
	PowerShellPath() string
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
	Register(Descriptor{
		Name:   NameWindowsShell,
		Parent: NameCommandExec,
		Assert: func(item any) bool { _, ok := item.(WindowsShellCapable); return ok },
	})
}
