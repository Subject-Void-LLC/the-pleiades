// Package transport defines the protocol-agnostic port through which
// Pleiades runs one command against one device. It knows nothing about
// inventory devices, capabilities, or any specific wire protocol; a
// concrete Adapter (internal/transport/ssh, for example) is what speaks a
// real protocol, and internal/engine is what translates a resolved
// device into the Target this package understands. Phase 16 places the
// same transport behind the runner mesh; it does not own the transport
// itself, which is exactly why this package stays decoupled from
// inventory and capability concepts today.
package transport

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

// Endpoint identifies what a Target actually connects to. Before Phase
// 73 this was always a network host:port pair, folded directly into
// Target's own Host/Port fields; Phase 73 gives it real siblings that
// are not a host and a port at all (a local serial line, a local IPC
// socket), so the choice needed a real type rather than another pair of
// fields on Target.
//
// It is a SEALED interface: endpoint() is unexported, so only this
// package can add a variant, and every Adapter type-switches on the
// concrete kind it understands and errors clearly on any other rather
// than guessing which fields apply. Two alternatives were rejected by
// name. A tagged struct (a Kind enum plus every field of every variant)
// makes invalid states representable: nothing would stop a caller
// building a "serial" Kind with Host set and no Device, and the compiler
// could not help. Encoding the variant into a string
// ("serial:/dev/ttyUSB0") is the stringly-typed smuggling AGENTS.md
// forbids outright, and it is exactly the shape that would let
// sshTransport.Exec hand such a string to net.JoinHostPort and produce a
// confusing dial error instead of a type error.
type Endpoint interface {
	// endpoint is the marker method that seals this interface to this
	// package.
	endpoint()
}

// NetworkEndpoint is a TCP host:port pair: the only Endpoint kind that
// existed before Phase 73, as Target's own Host/Port fields, moved
// behind this interface unchanged. Every Hop (below) is always a
// NetworkEndpoint, even when a chain's final Target.Endpoint is not: a
// bastion or jump host is always reached over TCP regardless of what it
// leads to.
type NetworkEndpoint struct {
	// Host is the address or hostname to connect to.
	Host string

	// Port is the TCP port to connect to.
	Port int
}

func (NetworkEndpoint) endpoint() {}

// SerialEndpoint identifies a local serial line: a directly attached
// USB-serial adapter, an onboard UART, or similar. Device and Line reuse
// pkg/serialline's own types unchanged rather than redeclaring them here
// — the same leaf-package resolution capability.SerialCapable's own doc
// comment describes for the identical need on the capability side.
type SerialEndpoint struct {
	Device serialline.Device
	Line   serialline.Config
}

func (SerialEndpoint) endpoint() {}

// SocketAddress identifies a local IPC endpoint: a Unix domain socket
// path on POSIX, or a Windows named pipe (e.g.
// "npipe:////./pipe/docker_engine"). A named string type, matching
// capability.SocketAddress's identical shape and identical reason
// (pkg/capability cannot import this package, and this package must not
// import pkg/capability, so the two are independent narrow types rather
// than one shared definition) — never parsed, joined, or validated as a
// POSIX path, since on Windows it is not one.
type SocketAddress string

// LocalSocketEndpoint identifies a local IPC endpoint reached with no
// intervening network hop: a Docker daemon socket, for example.
type LocalSocketEndpoint struct {
	Address SocketAddress
}

func (LocalSocketEndpoint) endpoint() {}

// DockerExecEndpoint identifies one container reached through a Docker
// daemon socket: the daemon's own LocalSocketEndpoint plus the specific
// container being exec'd into. A container id is deliberately not
// smuggled into LocalSocketEndpoint's own Address field — a container id
// is not itself an address, and doing so would reintroduce exactly the
// tagged-struct ambiguity this sealed interface exists to prevent.
//
// # Nothing constructs this today, and the reason is architectural
//
// No production code builds a DockerExecEndpoint and no Adapter's type
// switch accepts one. That is not an unfinished wiring job waiting to be
// completed: container.docker.exec is a Collection method rather than a
// TransportBinding, because the container id is a per-task parameter a
// binding's Target function (which sees only the device) cannot supply,
// and internal/catalog/container/docker/exec.go's own doc comment states
// that reasoning. It reaches the daemon through pkg/dockerexec directly.
//
// This variant is kept rather than deleted because Endpoint is a sealed
// interface: every implementation lives in this one package, so removing
// and later re-adding it is a single-file change with no external
// breakage either way, and the shape is the one a future
// transport-shaped container exec would want. It is disclosed here so a
// reader does not mistake an unreachable variant for a wiring gap, the
// same way pkg/tftpxfer and pkg/rfc2217 are disclosed in
// docs/10-running-in-production.md.
type DockerExecEndpoint struct {
	Socket      LocalSocketEndpoint
	ContainerID string
}

func (DockerExecEndpoint) endpoint() {}

// Target identifies where to connect: an Endpoint, plus an optional
// Route of intermediate network hops reached first. Target deliberately
// carries no knowledge of inventory devices or capabilities (no
// DeviceID, no capability.Name) beyond what a Hop names for its own
// credential lookup; translating a device (and its configured bastion
// chain, if any) into a Target is internal/engine's job, not this
// package's. Keeping Target this narrow is what lets Phase 16's runner
// mesh reuse this exact type, and every Adapter built against it.
type Target struct {
	// Endpoint identifies what this Target actually connects to. Every
	// Adapter built before Phase 73 only ever understood a
	// NetworkEndpoint; a caller building a Target by hand for one of
	// them still sets this field explicitly (Target{Endpoint:
	// NetworkEndpoint{Host: ..., Port: ...}}) rather than the bare
	// Host/Port fields this type held before Phase 73 — a deliberate,
	// planned break to the type, not an oversight, recorded once here
	// rather than claimed as backward-compatible the way Route was.
	Endpoint Endpoint

	// Route is an ordered list of hops to dial through before Endpoint
	// itself, first to last (a bastion, then a second jump host nested
	// behind it, and so on). An empty or nil Route is exactly a direct
	// connection: every Adapter built before Phase 72 keeps working
	// unedited on this field specifically, since a zero-value Target
	// already has a nil Route.
	Route []Hop
}

// Hop is one intermediate connection reached before a Target's own
// Host:Port: a bastion, a jump host, a management-network console server.
// A Hop is a real device with its own address and its own credential,
// never inherited from the Target it is reached on behalf of (a bastion's
// account and a production device's account are almost never the same,
// and treating them as interchangeable would risk sending the wrong
// secret to the wrong host).
type Hop struct {
	// Host is the hop's own address or hostname.
	Host string

	// Port is the hop's own TCP port.
	Port int

	// DeviceName identifies which device this hop's Credential was
	// resolved from (internal/credential.Store.Lookup, under this name),
	// so an error naming a hop names a real, addressable inventory item
	// rather than a bare address.
	DeviceName string

	// Credential authenticates to this hop. It is resolved independently
	// of the Target's own credential, by internal/engine, before this
	// Target is built: a hop with no stored credential of its own must
	// fail naming that hop, never silently fall back to the Target's.
	Credential credential.Credential
}

// Result is what running one command against one Target produced.
type Result struct {
	// Stdout is everything the remote command wrote to its standard
	// output stream.
	Stdout string

	// Stderr is everything the remote command wrote to its standard
	// error stream.
	Stderr string

	// ExitCode is the remote command's process exit status. A non-zero
	// ExitCode is NOT a Go error (see the Transport doc comment below);
	// it is reported here so the caller decides what a failed remote
	// command means for its own workflow. Meaningless when
	// ExitStatusUnknown is true — see that field's own doc comment.
	ExitCode int

	// ExitStatusUnknown is true when the underlying protocol has no
	// concept of a remote exit status at all, rather than merely having
	// reported zero. A serial console, a raw TCP byte pipe, and a bare
	// Telnet session (Phase 73) are all this shape: the device wrote
	// some bytes back, and that is everything anyone knows. When true,
	// ExitCode carries no information and a caller MUST NOT treat it as
	// zero meaning success — the device may have written
	// "% Invalid input detected" and reported nothing else that would
	// say so. Every Adapter that predates Phase 73 (SSH, WinRM) has a
	// real exit status and never sets this; Docker exec (Phase 73) has
	// one too and does not set it either. Set it, and only it, is what a
	// byte-stream Adapter with no exit status must do.
	ExitStatusUnknown bool
}

// Transport runs one command against one Target once, authenticating
// with cred. An implementation speaks exactly one wire protocol (SSH,
// WinRM, ...); the caller never needs to know which.
//
// The distinction between the two ways Exec can end is load-bearing and
// must be honored precisely by every implementation:
//
//   - A non-zero Result.ExitCode is NOT itself a Go error. It means
//     command reached target, ran to completion, and reported failure,
//     exactly like a shell command run interactively that exits nonzero.
//     Exec returns (Result, nil) in this case; the caller decides what a
//     failed remote command means (retry at a higher level, mark a task
//     failed, treat it as an expected outcome, ...).
//   - A non-nil error return means the command's outcome could not be
//     determined at all: the target could not be dialed, authentication
//     was rejected, the connection was lost mid-command, the context was
//     canceled before or during execution, or some other protocol-level
//     failure occurred. Result is meaningless when error is non-nil, and
//     the caller must not interpret a zero-value Result as "it worked."
type Transport interface {
	// Exec runs command against target once, authenticating with cred.
	// See the Transport doc comment above for exactly what a non-zero
	// Result.ExitCode versus a non-nil error each mean.
	Exec(ctx context.Context, target Target, cred credential.Credential, command string) (Result, error)
}
