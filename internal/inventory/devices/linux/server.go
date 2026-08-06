// Package linux holds the concrete device type implementation for
// standard Linux servers. This package's init registers NewServer into the
// shared record.Types registry under "linux_server";
// internal/inventory/factory.go's NewItemFactory draws its
// batteries-included set from that registry rather than importing this
// package by name (internal/inventory/builtins.go blank-imports it purely
// to trigger this init). This package itself never imports
// internal/inventory, only the leaf internal/inventory/record package plus
// pkg/inventory and pkg/capability, so internal/inventory can depend on
// this package (via the blank import) without a cycle back.
package linux

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/record"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/policy"
)

func init() {
	record.RegisterType("linux_server", NewServer)
}

// Server implements InventoryItem plus, structurally,
// capability.SSHTransportCapable and capability.LinuxCapable.
type Server struct {
	*record.Base
}

// NewServer builds a Server from rec. It matches the
// func(record.Record) (inventory.InventoryItem, error) shape ItemFactory's
// registry expects.
//
// The capability set is the vendor baseline (SSHTransportCapable,
// LinuxCapable) unioned with rec.Capabilities, per Phase 32's capability
// granularity decision: classification-derived data can only add to what
// this type already asserts about itself, never replace it -- a Record
// hydrated with no Classify path (an explicit Type) still gets the same
// baseline this constructor always granted, while a classified one can
// gain more (policy.UnionSlices, Section 25's shared primitive, rather
// than a bespoke dedup loop here).
func NewServer(rec record.Record) (inventory.InventoryItem, error) {
	caps := policy.UnionSlices(
		[]capability.Name{capability.NameSSHTransport, capability.NameLinux},
		rec.Capabilities,
	)
	base := record.NewBase(rec, caps)
	return &Server{Base: base}, nil
}

// HasCapability checks the declared classification AND the structural
// registry assertion, so a true result is a guarantee, not a hope.
func (l *Server) HasCapability(name capability.Name) bool {
	return l.Declares(name) && capability.Implements(l, name)
}

// SSHHost returns the configured management host for this server.
func (l *Server) SSHHost() string {
	host, _ := l.Properties().String("host")
	return host
}

// SSHPort returns the configured SSH port, defaulting to 22.
func (l *Server) SSHPort() int {
	if port, ok := l.Properties().Int("port"); ok && port != 0 {
		return port
	}
	return 22
}

// KernelVersion returns the detected Linux kernel version.
func (l *Server) KernelVersion() string {
	v, _ := l.Properties().String("kernel_version")
	return v
}

// Distribution returns the detected Linux distribution name.
func (l *Server) Distribution() string {
	v, _ := l.Properties().String("distribution")
	return v
}
