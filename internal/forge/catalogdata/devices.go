// This file holds the two new device types docs/hephaestus.md's catalog
// implies beyond the existing cisco.Router and linux.Server: a Windows
// server and an AWS-API-addressable controller-side target.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devicescaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
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
}
