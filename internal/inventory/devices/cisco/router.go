// Package cisco holds the concrete device type implementations for Cisco
// gear (currently IOS/IOS-XE routers). This package's init registers
// NewRouter into the shared record.Types registry under "cisco_router";
// internal/inventory/factory.go's NewItemFactory draws its
// batteries-included set from that registry rather than importing this
// package by name (internal/inventory/builtins.go blank-imports it purely
// to trigger this init). This package itself never imports
// internal/inventory, only the leaf internal/inventory/record package plus
// pkg/inventory and pkg/capability, so internal/inventory can depend on
// this package (via the blank import) without a cycle back.
package cisco

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/policy"
)

func init() {
	record.RegisterType("cisco_router", NewRouter)
}

// Router implements InventoryItem plus, structurally,
// capability.SSHTransportCapable, capability.CiscoIOSCapable, and
// capability.NetworkCLICapable (CiscoIOSCapable's Section 8 parent,
// docs/hephaestus.md's own worked example: net.cli.config, requiring the
// broad NetworkCLICapable, resolves down to net.ios.config against a
// Router at plan time).
type Router struct {
	*record.Base
}

// NewRouter builds a Router from rec. It matches the
// func(record.Record) (inventory.InventoryItem, error) shape ItemFactory's
// registry expects.
//
// The capability set is the vendor baseline (SSHTransportCapable,
// CiscoIOSCapable) unioned with rec.Capabilities, per Phase 32's capability
// granularity decision: classification-derived data can only add to what
// this type already asserts about itself, never replace it -- a Record
// hydrated with no Classify path (an explicit Type) still gets the same
// baseline this constructor always granted, while a classified one can
// gain more (policy.UnionSlices, Section 25's shared primitive, rather
// than a bespoke dedup loop here).
func NewRouter(rec record.Record) (inventory.InventoryItem, error) {
	caps := policy.UnionSlices(
		netconfBaseline(rec, []capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS, capability.NameNetworkAddressable}),
		rec.Capabilities,
	)
	base := record.NewBase(rec, caps)
	return &Router{Base: base}, nil
}

// HasCapability checks the declared classification AND the structural
// registry assertion, so a true result is a guarantee, not a hope.
func (c *Router) HasCapability(name capability.Name) bool {
	return c.Declares(name) && capability.Implements(c, name)
}

// SSHHost returns the configured management host for this router.
func (c *Router) SSHHost() string {
	host, _ := c.Properties().String("host")
	return host
}

// SSHPort returns the configured SSH port, defaulting to 22.
func (c *Router) SSHPort() int {
	if port, ok := c.Properties().Int("port"); ok && port != 0 {
		return port
	}
	return 22
}

// IPAddress returns this router's reachable network address, the same
// value SSHHost reports. Phase 73 added this accessor
// (capability.NetworkAddressableCapable) so pleiades.builtin.wait.port
// could dispatch against a real device at all -- see FAILURE_PATTERNS.md.
func (c *Router) IPAddress() string {
	return c.SSHHost()
}

// IOSVersion returns the detected or configured IOS firmware version.
func (c *Router) IOSVersion() string {
	v, _ := c.Properties().String("ios_version")
	return v
}

// SupportsNETCONF reports whether NETCONF/YANG is enabled on this router.
//
// It is redundant with NetconfPort below plus the netconf_enabled
// property netconfBaseline reads, and is kept rather than removed
// because it is a method on capability.CiscoIOSCapable: dropping it
// would change that interface, which every Cisco device type and every
// CiscoIOSCapable-requiring method depends on. That is a separate
// breaking change with its own blast radius, named here as debt rather
// than made a second one.
func (c *Router) SupportsNETCONF() bool {
	v, _ := c.Properties().Bool("netconf_enabled")
	return v
}

// NetconfPort returns the router's NETCONF listener port, the structural
// half of capability.NetconfCapable (netconfBaseline supplies the data
// half). It defaults to 830, matching SSHPort's own default-22 shape.
//
// The default is not a formality. NETCONF over SSH is a subsystem on an
// ordinary SSH connection, so reaching it on port 22 SHOULD work; a real
// Cisco IOS XE 17.12 device ACCEPTS the subsystem request on 22 and then
// immediately ends the channel, serving NETCONF only on 830. A client
// that assumed the SSH port would report that device as working, which
// is why this is a separate accessor rather than a reuse of SSHPort.
func (c *Router) NetconfPort() int {
	if port, ok := c.Properties().Int("netconf_port"); ok && port != 0 {
		return port
	}
	return 830
}

// CLIPrompt returns the router's configured CLI prompt string, the
// structural half of capability.NetworkCLICapable (CiscoIOSCapable's
// Section 8 parent -- see capability.Resolves for the data half).
func (c *Router) CLIPrompt() string {
	v, _ := c.Properties().String("cli_prompt")
	return v
}

// netconfBaseline returns the vendor capability baseline with
// capability.NameNetconf appended when this record's own properties say
// NETCONF is enabled on the device.
//
// This is the DATA half of NetconfCapable, and without it the
// NetconfPort accessor above would be useless. record.Base.HasCapability is
// Declares(name) AND capability.Implements(c, name), and NetconfCapable
// is not an ancestor of CiscoIOSCapable (it has no parent at all), so
// declaring CiscoIOSCapable does not resolve to it.
// Meanwhile "pleiades add-host" has no capability flag at all and a
// --type-created record carries a nil Capabilities slice, so nothing
// else could put the name there. A NetconfPort accessor on its own
// would therefore have produced a capability that internal/archtest
// reports as satisfiable (its probe hydrates every capability name) and
// that no real inventory item could ever be dispatched to.
//
// Keying it on netconf_enabled rather than granting it unconditionally
// is the honest reading of what the property means: NETCONF is
// configuration on a Cisco device, not a property of the model, and
// this same sandbox device answers NETCONF on port 830 while refusing to
// serve it on 22. It also gives the pre-existing SupportsNETCONF
// accessor a real job. That method was previously a bare boolean
// asserting a claim the type system could not check; the property it
// reads is now the classification data half of a capability whose
// structural half NetconfPort proves, which is how every other
// capability here works.
func netconfBaseline(rec record.Record, baseline []capability.Name) []capability.Name {
	if enabled, _ := inventory.NewProperties(rec.Properties).Bool("netconf_enabled"); enabled {
		return append(baseline, capability.NameNetconf)
	}
	return baseline
}
