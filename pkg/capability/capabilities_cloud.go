package capability

const (
	NameDocker Name = "DockerCapable"
	NameAWSAPI Name = "AWSAPICapable"
)

// SocketAddress identifies a local IPC endpoint: a Unix domain socket
// path on POSIX, or a Windows named pipe (e.g.
// "npipe:////./pipe/docker_engine"). It is a named string type per
// AGENTS.md's "prefer a named string type over a raw string" rule,
// specifically so a caller cannot silently pass it to path/filepath: on
// Windows the value is not a filesystem path at all, and even on POSIX
// it names a socket, not a file whose contents are ever opened, joined,
// or cleaned as one.
type SocketAddress string

// DockerCapable is satisfied by devices that can run containers via
// Docker.
type DockerCapable interface {
	// DockerEndpoint returns the address of the Docker control socket:
	// a Unix socket path on POSIX (e.g. "/var/run/docker.sock") or a
	// Windows named pipe (e.g. "npipe:////./pipe/docker_engine"). The
	// value is opaque and must never be parsed, joined, or validated as
	// a POSIX path - doing so is exactly wrong for the Windows case.
	DockerEndpoint() SocketAddress
}

// AWSAPICapable is satisfied by resources addressable through the AWS
// API rather than a direct transport (e.g. a controller-side target).
type AWSAPICapable interface {
	// AWSRegion returns the region the resource lives in.
	AWSRegion() string
}

func init() {
	Register(Descriptor{
		Name:   NameDocker,
		Assert: func(item any) bool { _, ok := item.(DockerCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameAWSAPI,
		Assert: func(item any) bool { _, ok := item.(AWSAPICapable); return ok },
	})
}
