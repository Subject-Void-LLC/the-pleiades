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
		},
	},
}
