package capability

const (
	NameNetworkCLI         Name = "NetworkCLICapable"
	NameNetconf            Name = "NetconfCapable"
	NameJunos              Name = "JunosCapable"
	NameAristaEOS          Name = "AristaEOSCapable"
	NameNetworkAddressable Name = "NetworkAddressableCapable"
	NameFileTransfer       Name = "FileTransferCapable"
)

// NetworkCLICapable is satisfied by any network device exposing a
// CLI/API surface a Collection can send structured commands to, without
// committing to a specific protocol or vendor. NetconfCapable,
// JunosCapable, and AristaEOSCapable are its floor-level children
// (PLAN.md Section 8 names all three together as capabilities "as
// specific as a capability ever gets").
type NetworkCLICapable interface {
	// CLIPrompt returns the device's CLI prompt string.
	CLIPrompt() string
}

// NetconfCapable is satisfied by devices reachable over NETCONF. It is a
// sibling of JunosCapable/AristaEOSCapable under NetworkCLICapable, not
// their parent: not every vendor CLI capability is NETCONF-based (Arista
// EOS in particular is not), so nesting them under NetconfCapable would
// assert something false.
type NetconfCapable interface {
	NetworkCLICapable

	// NetconfPort returns the device's NETCONF listener port (e.g. 830).
	NetconfPort() int
}

// JunosCapable is satisfied by devices running Juniper Junos.
type JunosCapable interface {
	NetworkCLICapable

	// JunosVersion returns the detected or configured Junos version.
	JunosVersion() string
}

// AristaEOSCapable is satisfied by devices running Arista EOS.
type AristaEOSCapable interface {
	NetworkCLICapable

	// EOSVersion returns the detected or configured EOS version.
	EOSVersion() string
}

// NetworkAddressableCapable is satisfied by any device reachable at a
// network address, independent of transport or protocol.
type NetworkAddressableCapable interface {
	// IPAddress returns the device's primary reachable address.
	IPAddress() string
}

// FileTransferCapable is satisfied by devices that support transferring
// files to or from them (e.g. SCP/SFTP).
type FileTransferCapable interface {
	// FileTransferRoot returns the base directory files are transferred
	// to and from.
	FileTransferRoot() string
}

func init() {
	Register(Descriptor{
		Name:   NameNetworkCLI,
		Assert: func(item any) bool { _, ok := item.(NetworkCLICapable); return ok },
	})
	Register(Descriptor{
		Name:   NameNetconf,
		Parent: NameNetworkCLI,
		Assert: func(item any) bool { _, ok := item.(NetconfCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameJunos,
		Parent: NameNetworkCLI,
		Assert: func(item any) bool { _, ok := item.(JunosCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameAristaEOS,
		Parent: NameNetworkCLI,
		Assert: func(item any) bool { _, ok := item.(AristaEOSCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameNetworkAddressable,
		Assert: func(item any) bool { _, ok := item.(NetworkAddressableCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameFileTransfer,
		Assert: func(item any) bool { _, ok := item.(FileTransferCapable); return ok },
	})
}
