package capability

const (
	NameDocker Name = "DockerCapable"
	NameAWSAPI Name = "AWSAPICapable"
)

// DockerCapable is satisfied by devices that can run containers via
// Docker.
type DockerCapable interface {
	// DockerSocketPath returns the path to the Docker control socket
	// (e.g. "/var/run/docker.sock").
	DockerSocketPath() string
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
