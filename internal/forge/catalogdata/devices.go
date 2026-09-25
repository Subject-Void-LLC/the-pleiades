// This file holds the two new device types docs/hephaestus.md's catalog
// implies beyond the existing cisco.Router and linux.Server: a Windows
// server and an AWS-API-addressable controller-side target.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// Devices is every device type Phase 34 generates via `pleiades forge
// new-device`, mirroring Collections' role as the single source of truth
// tools/gencatalog drives the real CLI from.
var Devices = []devicescaffold.Config{
	{
		// windows.Server mirrors linux.Server's identity+transport
		// baseline (LinuxCapable+SSHTransportCapable there becomes
		// WindowsCapable+WinRMCapable here) plus the two capabilities the
		// catalog's svc.windows.* and win.feature.* entries actually
		// require. TypeKey's final segment ("server") is what
		// devicescaffold turns into the exported struct name.
		Vendor:  "windows",
		TypeKey: "windows_server",
		Capabilities: []capability.Name{
			capability.NameWindows,
			capability.NameWinRM,
			capability.NameWindowsService,
			capability.NameWindowsFeature,
			capability.NameWindowsShell,
			capability.NameNetworkAddressable,
		},
	},
	{
		// aws.Account declares only AWSAPICapable, matching that
		// capability's own doc comment ("satisfied by resources
		// addressable through the AWS API rather than a direct
		// transport"). It deliberately declares no transport: an AWS
		// account has none, unlike an SSH- or WinRM-reachable device.
		Vendor:  "aws",
		TypeKey: "aws_account",
		Capabilities: []capability.Name{
			capability.NameAWSAPI,
		},
	},
	{
		// catalyst.Center is the Cisco Catalyst Center controller itself,
		// the target every net.catalyst.* method addresses. Like
		// aws.Account it declares an API capability and no transport,
		// because a controller answers REST calls rather than a terminal
		// session.
		//
		// It is a separate device type from the switches it manages, and
		// that split is the whole point: the controller is
		// CatalystAPICapable and nothing else, while the devices behind it
		// are SSH- and CLI-reachable Cisco gear that happens to have been
		// discovered through it. Modeling both as one type would force a
		// device to claim capabilities only one of them has.
		Vendor:  "catalyst",
		TypeKey: "catalyst_center",
		Capabilities: []capability.Name{
			capability.NameCatalystAPI,
		},
	},
	{
		// cisco.Switch is what the Catalyst Center sync plugin classifies
		// its managed IOS-XE devices as. cisco.Router already exists and is
		// hand-written; a switch is genuinely a different device type
		// (Section 6d's rule tree assigns them separately) rather than a
		// router with a different label, and the classification tree should
		// be able to say so.
		//
		// The capability set is the same identity+transport baseline
		// cisco.Router carries, plus NetworkCLICapable, which is what
		// net.cli.command and net.cli.config require and what a managed
		// switch actually offers.
		Vendor:  "cisco",
		TypeKey: "cisco_switch",
		Capabilities: []capability.Name{
			capability.NameSSHTransport,
			capability.NameCiscoIOS,
			capability.NameNetworkCLI,
			capability.NameNetworkAddressable,
		},
	},
	{
		// container.Host is Phase 73's fix for a real, previously-shipped
		// defect (FAILURE_PATTERNS.md #170): container.docker.run/stop/
		// remove declared RequiredCapabilities: []capability.Name{
		// capability.NameDocker} with zero device types able to satisfy
		// it. It carries SSHTransportCapable because those three methods
		// reach the Docker daemon through sdk.Connect's plain SSH session
		// (internal/catalog/container/docker's own package doc explains
		// why), DockerCapable for the capability the catalog actually
		// requires, and NetworkAddressableCapable for the same reason
		// every other network-reachable device type here now does.
		Vendor:  "container",
		TypeKey: "container_host",
		Capabilities: []capability.Name{
			capability.NameSSHTransport,
			capability.NameDocker,
			capability.NameNetworkAddressable,
		},
	},
	{
		// console.Device is the fix for the same class of defect
		// container.Host above fixed for Docker, found again one commit
		// later in Phase 73's own transport work: serial_exec,
		// serialtcp_exec and telnet_exec shipped as real bindings over
		// real transports, requiring SerialCapable, RawPassthroughCapable
		// and TelnetCapable, with zero device types able to satisfy any
		// of the three. See FAILURE_PATTERNS.md.
		//
		// It is the target whose only management path is a console: a
		// directly cabled serial line, a console/terminal server port
		// (raw TCP or RFC 2217), or bare Telnet. That covers gear with no
		// SSH at all (a legacy PBX, a channel bank, a PDU, a switch being
		// staged before its management address exists) and is deliberately
		// a separate type from cisco.Router or linux.Server rather than
		// four more capabilities bolted onto those: a Linux server reached
		// over SSH is not console-cabled, and claiming otherwise is the
		// overreach the Docker gap already taught this table to avoid.
		//
		// The capability list here is what this type can offer, which is
		// what devicescaffold's baseline means everywhere else in this
		// table. Unlike every other entry, the hand-completed constructor
		// does NOT declare all four unconditionally: these four are
		// alternative ways to reach one device, not four extra facts about
		// it, so each is declared only when the record actually carries
		// that path's configuration. internal/inventory/devices/console's
		// own package doc argues that at length.
		Vendor:  "console",
		TypeKey: "console_device",
		Capabilities: []capability.Name{
			capability.NameSerial,
			capability.NameRawPassthrough,
			capability.NameRFC2217,
			capability.NameTelnet,
		},
	},
}
