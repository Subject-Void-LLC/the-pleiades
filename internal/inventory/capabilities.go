package inventory

// SSHTransportCapable defines a contract for devices that can be accessed via SSH.
type SSHTransportCapable interface {
	SSHHost() string
	SSHPort() int
}

// CiscoIOSCapable defines a contract for devices running Cisco IOS/IOS-XE.
type CiscoIOSCapable interface {
	// IOSVersion returns the detected or configured firmware version.
	IOSVersion() string
	
	// SupportsNETCONF indicates if NETCONF/YANG is enabled on this router.
	SupportsNETCONF() bool
}

// LinuxCapable defines a contract for standard Linux servers.
type LinuxCapable interface {
	KernelVersion() string
	Distribution() string
}
