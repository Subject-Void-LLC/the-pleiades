package legacy

import "context"

// ContainerOrchestrator spins up one ephemeral, single-use container, runs
// it to completion, and returns its captured output. It is the Strategy
// PLAN.md Section 23.2 names ("The Controller instructs a Container
// Orchestrator (Kubernetes, AWS ECS)..."): DockerOrchestrator (this
// package) is the one real implementation Phase 17 (Legacy Ansible
// Adapter) builds, proven against a real Docker daemon rather than
// simulated, the same RULE 0 bar every other adapter in this repository is
// held to. A Kubernetes or ECS-backed implementation is Phase 26's
// (Execution Environments & Container Groups) named job, not this one's:
// nothing here or in Adapter depends on which ContainerOrchestrator a
// caller wires in, so adding a second implementation later requires no
// change to either.
type ContainerOrchestrator interface {
	// Run starts a container from spec, waits for it to exit on its own,
	// and returns its captured stdout, stderr, and exit code. A non-nil
	// error means the container could not be provisioned or observed at
	// all (a Docker daemon failure, an image pull failure); a container
	// that started and ran to completion but exited non-zero is reported
	// through ContainerResult.ExitCode, not through the error return, the
	// same split os/exec.Cmd.Run itself draws between "failed to start"
	// and "ran, exited non-zero".
	Run(ctx context.Context, spec ContainerSpec) (ContainerResult, error)
}

// ContainerSpec describes one ephemeral container run. It is the boundary
// between Adapter (which knows nothing about Docker, Kubernetes, or any
// other concrete orchestrator) and a ContainerOrchestrator implementation
// (which knows nothing about Ansible, wire.DispatchPayload, or job
// dispatch).
type ContainerSpec struct {
	// Image is a pinned image reference (never a "latest" tag; see
	// internal/testsupport/images.go's own stated rationale for why),
	// resolved by the caller, never built from any dispatch-derived
	// string.
	Image string

	// Argv is the full argument vector the container's entrypoint runs,
	// e.g. {"ansible-playbook", "-v", "-i", "/run/pleiades/inventory.json",
	// "/run/pleiades/playbook.yml"}. Every element is either a fixed,
	// compile-time constant or a path this package's own playbook/
	// inventory resolvers already validated; nothing here is ever a raw,
	// unvalidated dispatch-derived string passed through a shell (PLAN.md
	// Section 25's own Phase 39 audit names this exact discipline as a
	// standing requirement on the first phase to build an
	// ansible-playbook invocation, which is this one).
	Argv []string

	// Env is the container's environment. Never a place for a secret:
	// PLAN.md Section 17.5 forbids passing secrets through argv or the
	// process environment, and Section 29.4's resolution for the one
	// place that rule cannot hold inside Ansible itself (Ansible modules
	// read the environment) is that the ephemeral container is the trust
	// boundary and secrets cross via Files below, not Env.
	Env map[string]string

	// Files is copied into the container before its entrypoint runs. This
	// is how inventory.json (and, when present, an SSH private key) cross
	// the trust boundary: built as in-memory bytes, never written to the
	// Runner host's own disk, handed to the container orchestrator's own
	// local control channel (for DockerOrchestrator, the Docker daemon's
	// local socket).
	Files []ContainerFile

	// Networks names zero or more pre-existing container networks this
	// container should join, by name. Empty in every real dispatch: a
	// real target device is a real remote host reached over ordinary
	// networking, never another container needing Docker-network-level
	// attachment. It exists for this package's own Release Gate, which
	// (unlike production) really does run both ends of an SSH connection
	// as two ephemeral containers on the same Docker host, and needs them
	// on a shared network to reach each other portably (a Docker-Desktop
	// convenience like host.docker.internal cannot be relied on to work
	// identically on a native-Linux CI runner).
	Networks []string
}

// ContainerFile is one piece of in-memory content copied into a container
// before it starts.
type ContainerFile struct {
	// Content is the file's full bytes. Always in-memory, never a host
	// file path: see ContainerSpec.Files' own doc comment for why.
	Content []byte

	// ContainerPath is the absolute path Content is written to inside the
	// container.
	ContainerPath string

	// Mode is the file's permission bits, e.g. 0o600 for a file that may
	// carry a secret.
	Mode int64
}

// ContainerResult is a completed container run's captured output.
type ContainerResult struct {
	// Output is the container's combined stdout and stderr, in the order
	// each byte was originally written. A single combined stream, not a
	// Stdout/Stderr split: testcontainers-go's own Container.Logs already
	// demultiplexes Docker's wire-level stdout/stderr framing for a
	// non-TTY container (DockerContainer.parseMultiplexedLogs) into
	// exactly this shape, and re-splitting it back into two separate
	// streams would mean reimplementing that demuxing by hand against the
	// Docker daemon's raw log API for no real benefit: the stdout parser
	// this package builds already tolerates Ansible's own
	// `[WARNING]`/`[ERROR]` diagnostic lines interleaving with task
	// output (verified against real captured ansible-playbook output),
	// and Ansible itself writes nearly everything this package cares
	// about to stdout, with stderr carrying only a Python-level traceback
	// or a container-runtime-level failure, neither of which this parser
	// needs to attribute to a specific stream to translate correctly.
	Output []byte

	ExitCode int
}
