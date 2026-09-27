// Package container holds the concrete device type implementation for a
// Docker-capable host ("container_host"). Scaffolded by
// pleiades forge new-device (internal/inventory/devicescaffold), then
// hand-completed with real capability accessors, the same route
// pkg/winrmexec's own doc comment describes for a generated-then-completed
// package.
//
// This package's init registers NewHost into the shared record.Types
// registry under "container_host". internal/inventory/factory.go's
// NewItemFactory draws its batteries-included set from that registry
// rather than importing this package by name, so this package is NOT YET
// REACHABLE from the stock binary until internal/inventory/builtins.go
// carries a blank import of it (Phase 73's own step, done alongside this
// file). This package itself never imports internal/inventory, only the
// leaf internal/inventory/record package plus pkg/inventory,
// pkg/capability, and pkg/policy, so internal/inventory can depend on it
// (via that blank import) without a cycle back.
//
// # Why this device type exists
//
// container.docker.run/stop/remove (internal/catalog/container/docker)
// have been StatusImplemented and RequiredCapabilities:
// []capability.Name{capability.NameDocker} since Phase 34, but zero
// concrete device types implemented DockerCapable's accessor until this
// one: engine.checkMethodCapabilities refused every real invocation, and
// the package's own tests never caught it because they build their device
// as an inventorytest.Stub, which deliberately skips the structural
// assertion HasCapability performs on a real type. See
// FAILURE_PATTERNS.md for the full account. This type closes that gap for
// real, not just in a test double.
//
// Host also declares SSHTransportCapable, not DockerCapable alone,
// because container.docker.*'s own Invoke functions reach the daemon
// through sdk.Connect (pkg/sdk/connect.go), which type-asserts
// capability.SSHTransportCapable directly: these methods run "docker
// inspect"/"docker run"/"docker stop"/"docker rm" as plain shell commands
// over an SSH session to the host, the same tier pkg.apt.* and
// identity.user.* already ship at (internal/catalog/container/docker's
// own package doc says so). DockerEndpoint is a separate, independently
// satisfiable capability for a future exec-shaped Docker method that
// talks to the daemon socket directly instead (pkg/dockerexec, Phase 73's
// Workstream F) - declaring both is not redundant, since a device could
// in principle offer one without the other.
package container

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

func init() {
	record.RegisterType("container_host", NewHost)
}

// Host implements inventory.InventoryItem plus, structurally,
// capability.SSHTransportCapable and capability.DockerCapable.
type Host struct {
	*record.Base
}

var _ inventory.InventoryItem = (*Host)(nil)

// NewHost builds a Host from rec. It matches the
// record.Constructor shape (func(record.Record) (inventory.InventoryItem,
// error)) record.RegisterType expects.
//
// The capability set is the vendor baseline given to
// "pleiades forge new-device" unioned with rec.Capabilities, per Phase
// 32's capability granularity decision: classification-derived data can
// only add to what this type already asserts about itself, never replace
// it.
func NewHost(rec record.Record) (inventory.InventoryItem, error) {
	caps := policy.UnionSlices(
		[]capability.Name{
			capability.NameSSHTransport,
			capability.NameDocker,
			capability.NameNetworkAddressable,
		},
		rec.Capabilities,
	)
	base := record.NewBase(rec, caps)
	return &Host{Base: base}, nil
}

// HasCapability checks the declared classification AND the structural
// registry assertion, so a true result is a guarantee, not a hope.
func (c *Host) HasCapability(name capability.Name) bool {
	return c.Declares(name) && capability.Implements(c, name)
}

// SSHHost returns the configured management host for this container
// host, the same "host" property key linux.Server.SSHHost reads.
func (c *Host) SSHHost() string {
	host, _ := c.Properties().String("host")
	return host
}

// SSHPort returns the configured SSH port, defaulting to 22, the same
// convention linux.Server.SSHPort uses.
func (c *Host) SSHPort() int {
	if port, ok := c.Properties().Int("port"); ok && port != 0 {
		return port
	}
	return 22
}

// IPAddress returns this host's reachable network address, the same
// value SSHHost reports. Declared (capability.NetworkAddressableCapable)
// so pleiades.builtin.wait.port can dispatch against a real device.
func (c *Host) IPAddress() string {
	return c.SSHHost()
}

// DockerEndpoint returns the configured Docker control socket address:
// a Unix socket path on a POSIX container host, or a Windows named pipe.
// Read from the "docker_endpoint" property via the typed accessor
// (rejecting nothing here that String itself would not - an absent or
// malformed value simply reads back empty, which is a Host correctly
// declaring the capability without yet being independently reachable
// through it, exactly as an absent "host" leaves SSHHost empty above),
// defaulting to the conventional POSIX socket path when unset, so a
// container host needs no configuration at all for the common case.
func (c *Host) DockerEndpoint() capability.SocketAddress {
	if addr, ok := c.Properties().String("docker_endpoint"); ok && addr != "" {
		return capability.SocketAddress(addr)
	}
	return "/var/run/docker.sock"
}
