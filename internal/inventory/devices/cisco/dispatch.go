// The properties a Cisco device's constructor and accessors read, which
// are all of one that travels to a Runner.
package cisco

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// accessorProperties is shared by both types: a switch is a router with
// switching accessors, reading the same record. The legacy SSH flags travel
// too, since a Runner's connection to the device reads them
// (remoteexec.LegacyFor).
var accessorProperties = append([]string{"catalyst_software_version", "cli_prompt", "host", "ios_version", "netconf_enabled", "netconf_port", "port", "telnet_port", "transport", ConsoleServerProperty, ConsoleLineProperty},
	remoteexec.LegacyProperties()...)

func init() {
	record.RegisterDispatchProperties("cisco_router", accessorProperties...)
	record.RegisterDispatchProperties("cisco_switch", accessorProperties...)
}
