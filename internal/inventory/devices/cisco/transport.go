// How a Cisco device's CLI is reached: SSH, or bare Telnet for a device
// with no SSH server (a lab router before SSH is enabled on it).
package cisco

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The transport property's values.
const (
	transportSSH    = "ssh"
	transportTelnet = "telnet"
)

// managementBaseline returns baseline with the capability that reaches the
// device's CLI: SSHTransportCapable unless the record's transport property
// says telnet, then TelnetCapable instead. A telnet-only device is never
// declared reachable over SSH, so a method and a plan check both take the
// path that works. Any other transport is refused at hydration.
func managementBaseline(rec record.Record, baseline []capability.Name) ([]capability.Name, error) {
	transport, _ := inventory.NewProperties(rec.Properties).String("transport")
	switch transport {
	case "", transportSSH:
		return append(baseline, capability.NameSSHTransport), nil
	case transportTelnet:
		return append(baseline, capability.NameTelnet), nil
	}
	return nil, fmt.Errorf("cisco: transport %q is neither %s nor %s", transport, transportSSH, transportTelnet)
}

// telnetPort is the record's telnet_port, defaulting to 23.
func telnetPort(props inventory.Properties) int {
	if port, ok := props.Int("telnet_port"); ok && port != 0 {
		return port
	}
	return 23
}

// TelnetHost returns the router's management host, reached over Telnet
// when its transport is telnet.
func (c *Router) TelnetHost() string { return c.SSHHost() }

// TelnetPort returns the router's telnet_port, defaulting to 23.
func (c *Router) TelnetPort() int { return telnetPort(c.Properties()) }

// TelnetHost returns the switch's management host, reached over Telnet
// when its transport is telnet.
func (c *Switch) TelnetHost() string { return c.SSHHost() }

// TelnetPort returns the switch's telnet_port, defaulting to 23.
func (c *Switch) TelnetPort() int { return telnetPort(c.Properties()) }
