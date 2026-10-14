// Package container holds the concrete device type implementation for a
// host that runs a container engine ("container_host"). Scaffolded by
// pleiades forge new-device (internal/inventory/devicescaffold), then
// hand-completed with real capability accessors, the same route
// pkg/winrmexec's own doc comment describes for a generated-then-completed
// package.
//
// This package's init registers NewHost into the shared record.Types
// registry under "container_host". internal/inventory/factory.go's
// NewItemFactory draws its batteries-included set from that registry
// rather than importing this package by name, so this package is reachable
// from the stock binary only through internal/inventory/builtins.go's blank
// import of it. This package itself never imports internal/inventory, only
// the leaf internal/inventory/record package plus pkg/inventory,
// pkg/capability, and pkg/policy, so internal/inventory can depend on it
// (via that blank import) without a cycle back.
//
// # What the device says, and what onboarding proved
//
// A container host's baseline is the SSH login and its address, which the
// record states, so SSH methods work on it before anything else happens.
// That it runs Docker (capability.DockerCapable), where the engine's socket
// is, which engine answers there, and how a task reaches it
// (capability.EngineSocketCapable over the SSH login,
// capability.EngineLocalCapable through the machine's own socket) are
// facts only onboarding records (internal/inventory/onboard), in the
// reserved inventory.DiscoveredProperty, under record.DiscoveryRules
// registered here. A record that types the socket itself, the property
// "docker_endpoint", is refused: a typed path let whoever wrote the record
// aim the process running a task at any socket on its own machine (Phase
// 119a, finding S1). The discovery is bound to "host" and "port", so
// repointing a record voids it until it is onboarded again.
//
// A record with no "host" is the machine that runs pleiades itself, whose
// engine is reached through its own socket; only the Crawl tier's
// `pleiades onboard`, run on that machine, can prove that.
package container

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

// TypeHost is the device type this package registers.
const TypeHost = "container_host"

// EngineTLSProperty is the host:port an engine answers its API on over
// mutual TLS, for a container host reached that way rather than over SSH. It
// is an address the record states, like generic_http's base URL; onboarding
// proves an engine answers there, and the discovery is bound to it.
const EngineTLSProperty = "engine_tls"

// RefusedEndpointKey is the property a record once used to type the
// engine's socket. It is refused by name, so an old inventory fails loudly
// rather than having its value ignored.
const RefusedEndpointKey = "docker_endpoint"

func init() {
	record.RegisterType(TypeHost, NewHost)
	record.RegisterDiscoveryRules(TypeHost, record.DiscoveryRules{
		Grants: engines.Grants,
		Bound:  append([]string{"host", "port", EngineTLSProperty}, devicetls.Properties()...),
	})
}

// Host implements inventory.InventoryItem plus, structurally,
// capability.SSHTransportCapable, the engine capabilities and every engine
// reach capability. Which of them it declares is its baseline and its
// discovery.
type Host struct {
	*record.Base
	engines.Accessors
	tls   devicetls.Settings
	stale string
}

var _ inventory.InventoryItem = (*Host)(nil)

// NewHost builds a Host from rec. It matches the
// record.Constructor shape (func(record.Record) (inventory.InventoryItem,
// error)) record.RegisterType expects.
//
// The capability set is the baseline unioned with rec.Capabilities, per
// Phase 32's capability granularity decision, plus what rec's discovery
// proved. A classification rule may add to the baseline but never grant
// what only onboarding may: those are dropped from rec.Capabilities before
// the union, since a rule is a claim nobody verified.
func NewHost(rec record.Record) (inventory.InventoryItem, error) {
	if _, present := rec.Properties[RefusedEndpointKey]; present {
		return nil, fmt.Errorf("%s %s: property %s is no longer set by hand; remove it and run `pleiades onboard %s`, which finds the engine's socket on the device",
			TypeHost, rec.Name, RefusedEndpointKey, rec.Name)
	}
	settings, err := devicetls.Parse(inventory.NewProperties(rec.Properties))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeHost, rec.Name, err)
	}
	classified := engines.WithoutGrants(rec.Capabilities)
	baseline := policy.UnionSlices(
		[]capability.Name{
			capability.NameSSHTransport,
			capability.NameNetworkAddressable,
		},
		classified,
	)
	caps, stale, err := record.Declared(TypeHost, rec, baseline)
	if err != nil {
		return nil, err
	}
	base := record.NewBase(rec, caps)
	return &Host{Base: base, Accessors: engines.Accessors{Of: base}, tls: settings, stale: stale}, nil
}

// HasCapability checks the declared classification AND the structural
// registry assertion, so a true result is a guarantee, not a hope.
func (c *Host) HasCapability(name capability.Name) bool {
	return c.Declares(name) && capability.Implements(c, name)
}

// DiscoveryBinding implements inventory.DiscoveryBinder: the digest a
// discovery must carry to hold for this record as it is now.
func (c *Host) DiscoveryBinding() string {
	return record.DiscoveryBinding(TypeHost, c.Properties())
}

// StaleDiscovery implements inventory.StaleDiscoverer.
func (c *Host) StaleDiscovery() string {
	return c.stale
}

// SSHHost returns the configured management host for this container
// host, the same "host" property key linux.Server.SSHHost reads. It is
// empty for the machine that runs pleiades itself.
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

// TLSSettings implements devicetls.Configured, so the TLS carrier verifies
// the engine against the authority the record pins rather than the system's.
func (c *Host) TLSSettings() devicetls.Settings { return c.tls }

// EngineTLSAddress implements capability.EngineTLSCapable: the host:port
// the record says the engine answers on over mutual TLS.
func (c *Host) EngineTLSAddress() string {
	addr, _ := c.Properties().String(EngineTLSProperty)
	return addr
}
