// generic_netconf: a device configured over NETCONF and nothing else.
package generic

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// defaultNetconfPort is RFC 6242's NETCONF over SSH port.
const defaultNetconfPort = 830

// Netconf is generic_netconf: a device configured over NETCONF (RFC 6241)
// and nothing else this platform knows of. It has no command line, so it
// never satisfies net.cli.* (FAILURE_PATTERNS 345).
//
// NetconfCapable itself is discovered: onboarding opens a session and
// reads the server's hello, and only a completed hello grants it.
type Netconf struct {
	*record.Base
}

// NewNetconf builds a generic_netconf device from rec.
func NewNetconf(rec record.Record) (inventory.InventoryItem, error) {
	caps, err := declared(TypeNetconf, rec, []capability.Name{
		capability.NameSSHTransport,
		capability.NameNetworkAddressable,
	})
	if err != nil {
		return nil, err
	}
	return &Netconf{Base: record.NewBase(rec, caps)}, nil
}

// HasCapability checks the declared set AND the structural assertion.
func (n *Netconf) HasCapability(name capability.Name) bool {
	return n.Declares(name) && capability.Implements(n, name)
}

// SSHHost returns the device's host property.
func (n *Netconf) SSHHost() string {
	host, _ := n.Properties().String("host")
	return host
}

// SSHPort returns the port property, or the NETCONF port: the only SSH
// service such a device is known to run is NETCONF's own.
func (n *Netconf) SSHPort() int {
	if port, ok := n.Properties().Int("port"); ok && port != 0 {
		return port
	}
	return n.NetconfPort()
}

// NetconfPort returns the netconf_port property, defaulting to 830.
func (n *Netconf) NetconfPort() int {
	if port, ok := n.Properties().Int("netconf_port"); ok && port != 0 {
		return port
	}
	return defaultNetconfPort
}

// IPAddress returns the host the device is reached at.
func (n *Netconf) IPAddress() string {
	return n.SSHHost()
}
