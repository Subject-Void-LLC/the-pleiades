package capability

const (
	NameNetworkCLI         Name = "NetworkCLICapable"
	NameNetconf            Name = "NetconfCapable"
	NameJunos              Name = "JunosCapable"
	NameAristaEOS          Name = "AristaEOSCapable"
	NameNetworkAddressable Name = "NetworkAddressableCapable"
	NameFileTransfer       Name = "FileTransferCapable"
	NameCatalystAPI        Name = "CatalystAPICapable"
)

// NetworkCLICapable is satisfied by any network device exposing a
// CLI/API surface a Collection can send structured commands to, without
// committing to a specific protocol or vendor. NetconfCapable,
// JunosCapable, and AristaEOSCapable are its floor-level children: as
// specific as a capability ever gets.
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

// FileTransferCapable is satisfied by devices that files can be moved to
// and from, confined to one directory: over SFTP (pkg/sftpxfer) or
// legacy SCP (pkg/scpxfer), both behind the pkg/filexfer port.
//
// # Who satisfies it, and who uses it
//
// linux_server declares it when its inventory record sets the
// file_transfer_root property, and refuses to load when that property
// is set to something unusable; a server with no root does not claim
// it. No Collection method or transport fqcn requires it yet: the
// transfer packages are libraries a method will be built on, and
// docs/10-running-in-production.md says so beside its disclosure of
// what they do and do not protect against.
type FileTransferCapable interface {
	// FileTransferRoot returns the one directory transfers to and from
	// the device are confined to, as an absolute, slash-separated,
	// canonical path, or the empty string when none is configured, which
	// filexfer.Resolve refuses. Every transfer path is resolved against
	// it before anything is dialed.
	FileTransferRoot() string
}

// CatalystAPICapable is satisfied by a Cisco Catalyst Center controller
// addressable through its REST API rather than a direct transport, the
// same shape AWSAPICapable already establishes for a controller-side
// target.
//
// It is deliberately a sibling of NetworkCLICapable rather than a child.
// The devices a Catalyst Center manages are NetworkCLICapable (and
// CiscoIOSCapable, and SSHTransportCapable); the controller itself is
// none of those. It has no CLI prompt and nothing sends it configuration
// over a terminal session. Nesting it under NetworkCLICapable would let
// capability.Resolves answer "yes" to a question about the controller
// that is only true of the switches behind it.
//
// It also sits exactly at the vendor-API-family floor and no lower. A
// Catalyst Center's software version, its deployment size, and the
// specific hardware models it manages are all platform-target data
// matched against classification facts, never new capabilities: growing
// the vocabulary per model or per release is the unchecked-boolean-claim
// anti-pattern the capability floor exists to prevent.
type CatalystAPICapable interface {
	// CatalystBaseURL returns the controller's API base URL.
	CatalystBaseURL() string
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
	Register(Descriptor{
		Name:   NameCatalystAPI,
		Assert: func(item any) bool { _, ok := item.(CatalystAPICapable); return ok },
	})
}
