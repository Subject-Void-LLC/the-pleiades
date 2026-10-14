// The container engines a device runs, and the ways a task reaches them.
// Each is a capability onboarding proves and records, never one a record is
// given, so which engine a task uses and how it reaches it are what the
// device advertised.
package capability

const (
	// NameContainerEngine names a device that runs at least one container
	// engine. It is the parent of NameDocker and NamePodman, so an
	// engine-agnostic method requires it and an engine-only method requires
	// the child.
	NameContainerEngine Name = "ContainerEngineCapable"

	// NamePodman names a device that runs Podman.
	NamePodman Name = "PodmanCapable"

	// NameEngineLocal names the reach through the executor's own socket.
	NameEngineLocal Name = "EngineLocalCapable"

	// NameEngineSocket names the reach through an SSH channel to the
	// engine's socket on the device.
	NameEngineSocket Name = "EngineSocketCapable"

	// NameEngineStdio names the reach through an SSH exec channel running
	// the engine's own `system dial-stdio`.
	NameEngineStdio Name = "EngineStdioCapable"

	// NameEngineTLS names the reach through TCP with mutual TLS.
	NameEngineTLS Name = "EngineTLSCapable"
)

// EngineKind is a container engine this platform speaks to.
type EngineKind string

// The engines. Podman is reached through its Docker-compatible API for the
// calls both engines share, and through its own for what only it has.
const (
	EngineDocker EngineKind = "docker"
	EnginePodman EngineKind = "podman"
)

// Engines lists every engine kind, in the order a device's engines are
// reported.
func Engines() []EngineKind { return []EngineKind{EngineDocker, EnginePodman} }

// Carrier is one way a task's requests reach an engine's API.
type Carrier string

// The carriers, in the order a method takes the first one a device
// advertises for an engine: no network hop first, then the SSH login every
// other task uses, then TLS.
const (
	CarrierLocal  Carrier = "local"
	CarrierSocket Carrier = "socket"
	CarrierStdio  Carrier = "stdio"
	CarrierTLS    Carrier = "tls"
)

// Carriers lists every carrier in the order a method prefers them.
func Carriers() []Carrier {
	return []Carrier{CarrierLocal, CarrierSocket, CarrierStdio, CarrierTLS}
}

// Transport is the transport a task uses when it reaches an engine over c
// (TransportEngineLocal and the rest).
func (c Carrier) Transport() string { return "engine_" + string(c) }

// EngineID is the identity onboarding pinned for an engine, which every
// later connection reads again and refuses when it differs, the way a pinned
// host key refuses another machine. For Docker it is the engine's own "ID"
// from GET /info, which persists across restarts. Podman reports no stable
// identity (its compatible /info answers a new random ID on every request,
// measured 2026-10-13 against Podman 5.8.7), so for Podman it is a digest of
// the container store's location as the engine reports it: host name, graph
// root and run root, which is what a daemonless engine's identity is.
type EngineID string

// ContainerEngineCapable is satisfied by a device that runs container
// engines. ContainerEngines lists the ones onboarding found, in Engines'
// order.
type ContainerEngineCapable interface {
	ContainerEngines() []EngineKind
}

// DockerCapable is satisfied by devices that run containers with Docker.
// It says what the device is; how a task reaches the engine is a separate
// capability (EngineLocalCapable, EngineSocketCapable, EngineStdioCapable,
// EngineTLSCapable), and onboarding proves both.
type DockerCapable interface {
	ContainerEngineCapable

	// DockerEndpoint returns the address of the Docker control socket on
	// the device, as onboarding proved it: a Unix socket path on POSIX
	// (e.g. "/var/run/docker.sock"). It is never a value a record was
	// given, since a typed path would let whoever writes the record aim
	// the process running a task at any socket on its own machine.
	DockerEndpoint() SocketAddress
}

// PodmanCapable is satisfied by devices that run containers with Podman.
type PodmanCapable interface {
	ContainerEngineCapable

	// PodmanEndpoint returns Podman's API socket on the device as onboarding
	// proved it, or the empty string when the engine is reached only through
	// a service started for each session (EngineStdioCapable).
	PodmanEndpoint() SocketAddress
}

// EngineReach is the part every reach capability shares: for each engine,
// the socket onboarding proved, the identity it pinned, and the carriers
// that worked, in Carriers' order.
type EngineReach interface {
	// EngineSocket returns the engine's socket path on the device.
	EngineSocket(kind EngineKind) SocketAddress

	// EngineID returns the identity onboarding pinned for the engine.
	EngineID(kind EngineKind) EngineID

	// EngineCarriers returns the carriers onboarding proved for the engine.
	EngineCarriers(kind EngineKind) []Carrier

	// EngineLoginUser returns the account onboarding's probe ran as, whose
	// engines, sockets and carriers the accessors above describe: a rootless
	// engine is one user's, and a rootful engine's socket opens for some
	// accounts and not others. Empty for an engine reached only over TLS,
	// where no account logs in.
	EngineLoginUser() string
}

// EngineLocalCapable is satisfied by a device whose container engine the
// process running a task reaches through its own Unix socket: the machine
// that runs the task is the device. Onboarding grants it only to a record
// that names no remote host, and only where the probe ran on that machine.
type EngineLocalCapable interface {
	EngineReach
}

// EngineSocketCapable is satisfied by a device whose container engine is
// reached through an SSH channel to the engine's socket on the device
// (direct-streamlocal@openssh.com), on the same login every other SSH task
// uses. Onboarding grants it only after opening that channel as the login
// user and reading the engine's identity through it.
type EngineSocketCapable interface {
	SSHTransportCapable
	EngineReach
}

// EngineStdioCapable is satisfied by a device whose container engine is
// reached through an SSH exec channel running the engine's own
// `system dial-stdio`, which works where sshd refuses forwarding. For Podman
// with no API service listening, the session starts one that exits on its
// own when idle.
type EngineStdioCapable interface {
	SSHTransportCapable
	EngineReach
}

// EngineTLSCapable is satisfied by a device whose container engine answers
// its API over TCP with mutual TLS. Plain TCP is never a carrier: an engine's
// API is root on its host, and no engine is too old for TLS.
type EngineTLSCapable interface {
	EngineReach

	// EngineTLSAddress returns the host:port the engine's API listens on.
	EngineTLSAddress() string
}

func init() {
	Register(Descriptor{
		Name:   NameContainerEngine,
		Assert: func(item any) bool { _, ok := item.(ContainerEngineCapable); return ok },
	})
	Register(Descriptor{
		Name:   NamePodman,
		Parent: NameContainerEngine,
		Assert: func(item any) bool { _, ok := item.(PodmanCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameEngineLocal,
		Assert: func(item any) bool { _, ok := item.(EngineLocalCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameEngineSocket,
		Assert: func(item any) bool { _, ok := item.(EngineSocketCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameEngineStdio,
		Assert: func(item any) bool { _, ok := item.(EngineStdioCapable); return ok },
	})
	Register(Descriptor{
		Name:   NameEngineTLS,
		Assert: func(item any) bool { _, ok := item.(EngineTLSCapable); return ok },
	})
}
