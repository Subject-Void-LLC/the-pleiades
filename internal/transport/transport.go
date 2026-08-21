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
)

// Target identifies where to connect: a host and a port, nothing more,
// plus an optional Route of intermediate hops reached first. Target
// deliberately carries no knowledge of inventory devices or capabilities
// (no DeviceID, no capability.Name) beyond what a Hop names for its own
// credential lookup; translating a device (and its configured bastion
// chain, if any) into a Target is internal/engine's job, not this
// package's. Keeping Target this narrow is what lets Phase 16's runner
// mesh reuse this exact type, and every Adapter built against it,
// unchanged.
type Target struct {
	// Host is the address or hostname to connect to.
	Host string

	// Port is the TCP port to connect to.
	Port int

	// Route is an ordered list of hops to dial through before Host:Port
	// itself, first to last (a bastion, then a second jump host nested
	// behind it, and so on). An empty or nil Route is exactly a direct
	// connection: every Adapter built before Phase 72 keeps working
	// unedited, since a zero-value Target already has a nil Route.
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
	// command means for its own workflow.
	ExitCode int
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
