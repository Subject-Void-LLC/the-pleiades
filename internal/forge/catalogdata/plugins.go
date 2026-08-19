// This file holds the inventory sync plugins Phase 34 generates, mirroring
// devices.go's role for device types and collections.go's for Collection
// methods: one hand-maintained data table that tools/gencatalog drives the
// real CLI from, never a hand-written generated package.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/pluginscaffold"
)

// Plugins is every inventory sync plugin generated via `pleiades forge
// new-plugin`.
//
// static_yaml is deliberately absent. It predates this table, it is
// hand-written rather than generated, and regenerating it would overwrite a
// real implementation with a skeleton. A plugin belongs here when the Forge
// created it; a plugin that was written before the Forge existed stays
// where it is, the same way cisco.Router and linux.Server are absent from
// Devices.
var Plugins = []pluginscaffold.Config{
	{
		// The first real dynamic sync plugin, and the reason the four-method
		// port exists at all: a static file was never enough evidence to
		// design one around.
		//
		// The endpoint is Cisco's public DevNet always-on sandbox, which is
		// what .SPECIFICATION/.CATALYST_CENTER_PLUGINS.md supplies and what
		// this plugin is verified against. It is a default, not a
		// hardcoding: any deployment overrides it with its own controller,
		// and the credential is never baked in at all (Config carries only
		// a credential name, resolved through internal/credential at
		// Connect time).
		Name:        "catalyst_center",
		Description: "reads managed network devices from a Cisco Catalyst Center",
		Endpoint:    "https://sandboxdnac.cisco.com",
		// The DevNet sandbox is read-only, and so is this plugin by design:
		// a controller is the authoritative source for the devices it
		// manages, so Pleiades imports from it and never writes back.
		ReadOnly: true,
	},
	{
		// The second real dynamic sync plugin, and the AWS-side answer to
		// AWX_PARITY.md's own inventory-sources row, which names AWS
		// alongside NetBox, Nautobot and VMware. Discovers EC2 instances
		// and hydrates them as ordinary linux_server devices, the same way
		// catalyst_center hydrates real Cisco gear.
		//
		// Endpoint is deliberately empty: unlike Catalyst Center, AWS has
		// no fixed public sandbox to default to, so an empty Endpoint
		// (meaning "real AWS") is the honest default, per
		// pluginscaffold.Config's own doc comment that Endpoint "may be
		// empty for a plugin whose endpoint is always per-deployment."
		Name:        "aws",
		Description: "reads EC2 instances from an AWS account/region",
		Endpoint:    "",
		// This plugin only ever calls DescribeInstances; there is no
		// write-back path to guard, the same structural guarantee
		// catalyst_center's own ReadOnly documents.
		ReadOnly: true,
	},
}
