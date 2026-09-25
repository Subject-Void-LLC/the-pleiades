// generic_ssh: any device reachable over SSH that runs commands.
package generic

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// SSH is generic_ssh: any device reachable over SSH that runs commands.
//
// Its baseline is what reaching it over SSH at all proves: the transport,
// running a command, and an address. Everything else, down to having a
// POSIX shell, is discovered by a fixed probe (onboarding), because an
// SSH login can land in a router's command line as easily as in a Linux
// shell.
//
// It embeds linux.Server for that type's accessors, the property names
// and defaults a Linux server's methods read (host, port, shell,
// service_manager and the rest), so the two types cannot drift apart. The
// embedding supplies accessors only: the declared set is this type's own,
// and a linux_server's baseline is never inherited.
type SSH struct {
	*linux.Server
}

// NewSSH builds a generic_ssh device from rec.
func NewSSH(rec record.Record) (inventory.InventoryItem, error) {
	caps, err := declared(TypeSSH, rec, []capability.Name{
		capability.NameSSHTransport,
		capability.NameCommandExec,
		capability.NameNetworkAddressable,
	})
	if err != nil {
		return nil, err
	}
	return &SSH{Server: &linux.Server{Base: record.NewBase(rec, caps)}}, nil
}

// HasCapability checks the declared set AND the structural assertion
// against this type, not the embedded one.
func (s *SSH) HasCapability(name capability.Name) bool {
	return s.Declares(name) && capability.Implements(s, name)
}

// KernelVersion returns the kernel release onboarding read, unless the
// record sets kernel_version itself.
func (s *SSH) KernelVersion() string {
	if v := s.Server.KernelVersion(); v != "" {
		return v
	}
	return discoveredFact(s.Properties(), "kernel_release")
}

// Distribution returns the distribution onboarding read from os-release,
// unless the record sets distribution itself.
func (s *SSH) Distribution() string {
	if v := s.Server.Distribution(); v != "" {
		return v
	}
	return discoveredFact(s.Properties(), "os_id")
}
