// How a Cisco device's CLI is reached: SSH, bare Telnet for a device with
// no SSH server (a lab router before SSH is enabled on it), or a console
// server for a device with no address of its own (a CML node reached
// through its controller).
package cisco

import (
	"fmt"
	"regexp"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The transport property's values.
const (
	transportSSH     = "ssh"
	transportTelnet  = "telnet"
	transportConsole = "console"
)

// The properties a console-served device names its console with.
const (
	// ConsoleServerProperty names the inventory device whose console server
	// reaches this one (a cml_controller).
	ConsoleServerProperty = "console_server"
	// ConsoleLineProperty is this device's console on that server: a CML
	// console's UUID, which names exactly one node's line.
	ConsoleLineProperty = "console_line"
)

// consoleUUID is what a console_line must be: it is written to the console
// server's prompt, so nothing but a UUID ever reaches it.
var consoleUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// deviceName is what a console_server must look like: an inventory name.
var deviceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// managementBaseline returns baseline with the capability that reaches the
// device's CLI: SSHTransportCapable unless the record's transport property
// says telnet, then TelnetCapable instead, or console, then
// ConsoleServedCapable with NetworkAddressableCapable taken out, since the
// device has no address of its own. A device is never declared reachable
// a way it is not, so a method and a plan check both take the path that
// works. Any other transport is refused at hydration, and so is a console
// device that names no console server or a line that is not a UUID.
func managementBaseline(rec record.Record, baseline []capability.Name) ([]capability.Name, error) {
	props := inventory.NewProperties(rec.Properties)
	transport, _ := props.String("transport")
	switch transport {
	case "", transportSSH:
		return append(baseline, capability.NameSSHTransport), nil
	case transportTelnet:
		return append(baseline, capability.NameTelnet), nil
	case transportConsole:
		server, _ := props.String(ConsoleServerProperty)
		line, _ := props.String(ConsoleLineProperty)
		switch {
		case !deviceName.MatchString(server):
			return nil, fmt.Errorf("cisco: transport console needs %s, the inventory name of the device whose console server reaches this one", ConsoleServerProperty)
		case !consoleUUID.MatchString(line):
			return nil, fmt.Errorf("cisco: transport console needs %s, the console's UUID (import-cml --via console writes it)", ConsoleLineProperty)
		}
		out := make([]capability.Name, 0, len(baseline)+1)
		for _, name := range baseline {
			if name != capability.NameNetworkAddressable {
				out = append(out, name)
			}
		}
		return append(out, capability.NameConsoleServed), nil
	}
	return nil, fmt.Errorf("cisco: transport %q is neither %s, %s nor %s", transport, transportSSH, transportTelnet, transportConsole)
}

// ConsoleServer returns the device whose console server reaches this
// router, when its transport is console.
func (c *Router) ConsoleServer() string {
	s, _ := c.Properties().String(ConsoleServerProperty)
	return s
}

// ConsoleLine returns this router's console on that server.
func (c *Router) ConsoleLine() string { s, _ := c.Properties().String(ConsoleLineProperty); return s }

// ConsoleServer returns the device whose console server reaches this
// switch, when its transport is console.
func (c *Switch) ConsoleServer() string {
	s, _ := c.Properties().String(ConsoleServerProperty)
	return s
}

// ConsoleLine returns this switch's console on that server.
func (c *Switch) ConsoleLine() string { s, _ := c.Properties().String(ConsoleLineProperty); return s }

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
