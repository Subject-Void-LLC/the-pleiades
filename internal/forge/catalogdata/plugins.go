// This file holds the inventory sync plugins Phase 34 generates, mirroring
// devices.go's role for device types and collections.go's for Collection
// methods: one hand-maintained data table that tools/gencatalog drives the
// real CLI from, never a hand-written generated package.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/pluginscaffold"
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
}
