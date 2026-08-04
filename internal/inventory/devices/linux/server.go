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
func NewServer(rec record.Record) (inventory.InventoryItem, error) {
	base := record.NewBase(rec, []capability.Name{capability.NameSSHTransport, capability.NameLinux})
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
