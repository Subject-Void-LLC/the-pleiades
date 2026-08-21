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
		[]capability.Name{capability.NameSSHTransport, capability.NameCiscoIOS, capability.NameNetworkAddressable},
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
func (c *Router) SupportsNETCONF() bool {
	v, _ := c.Properties().Bool("netconf_enabled")
	return v
}

// CLIPrompt returns the router's configured CLI prompt string, the
// structural half of capability.NetworkCLICapable (CiscoIOSCapable's
// Section 8 parent -- see capability.Resolves for the data half).
func (c *Router) CLIPrompt() string {
	v, _ := c.Properties().String("cli_prompt")
	return v
}
