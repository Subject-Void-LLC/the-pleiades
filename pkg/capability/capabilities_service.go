package capability

const (
	NameServiceManager Name = "ServiceManagerCapable"
	NameSystemd        Name = "SystemdCapable"
	NameFirewalld      Name = "FirewalldCapable"
	NameWindowsService Name = "WindowsServiceCapable"
)

// ServiceManagerCapable is satisfied by any device with some OS-level
// service manager, without committing to which one. SystemdCapable
// (Linux) and WindowsServiceCapable (Windows) are its two floor-level
// children, letting a Collection method like "svc.restart" target this
// broad capability and resolve downward on either OS family.
type ServiceManagerCapable interface {
	// ServiceManagerName returns the service manager's identifying name
	// (e.g. "systemd", "windows_scm").
	ServiceManagerName() string
}

// SystemdCapable is satisfied by devices managed through systemd.
type SystemdCapable interface {
	ServiceManagerCapable

	// SystemdUnitPath returns the directory systemd unit files live in.
	SystemdUnitPath() string
}

// FirewalldCapable is satisfied by devices running firewalld as a systemd
// unit. Its parent is SystemdCapable, not ServiceManagerCapable directly:
// firewalld is specifically a systemd-managed service, and this three-
// level chain (ServiceManagerCapable -> SystemdCapable -> FirewalldCapable)
// is this package's worked example of multi-hop hierarchy resolution.
type FirewalldCapable interface {
	SystemdCapable

	// FirewalldZone returns the device's default firewalld zone.
	FirewalldZone() string
}

// WindowsServiceCapable is satisfied by devices managed through the
// Windows Service Control Manager, sibling to SystemdCapable under
// ServiceManagerCapable.
type WindowsServiceCapable interface {
	ServiceManagerCapable

	// WindowsServiceStartMode returns the default service start mode
	// (e.g. "Automatic", "Manual").
	WindowsServiceStartMode() string
}

func init() {
	Register(Descriptor{
		Name:   NameServiceManager,
		Assert: func(item any) bool { _, ok := item.(ServiceManagerCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameSystemd,
		Parent: NameServiceManager,
		Assert: func(item any) bool { _, ok := item.(SystemdCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameFirewalld,
		Parent: NameSystemd,
		Assert: func(item any) bool { _, ok := item.(FirewalldCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameWindowsService,
		Parent: NameServiceManager,
		Assert: func(item any) bool { _, ok := item.(WindowsServiceCapable); return ok },
	})
}
