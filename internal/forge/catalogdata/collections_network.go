// This file holds the Network devices section of docs/hephaestus.md's
// catalog: ansible.netcommon.cli_command/cli_config/netconf_config, and
// the three vendor-specific config modules (cisco.ios, junipernetworks.junos,
// arista.eos). RequiresElevation does not map cleanly onto a network OS's
// own enable-mode privilege model, so every entry here leaves it false
// rather than asserting a Linux-specific semantic onto network gear.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

var networkCollections = []collectionscaffold.Config{
	{
		Name:          "net.cli.command",
		Capabilities:  []capability.Name{capability.NameNetworkCLI},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.cli.config",
		Capabilities:  []capability.Name{capability.NameNetworkCLI},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.netconf.config",
		Capabilities:  []capability.Name{capability.NameNetconf},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.ios.config",
		Capabilities:  []capability.Name{capability.NameCiscoIOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.junos.config",
		Capabilities:  []capability.Name{capability.NameJunos},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.eos.config",
		Capabilities:  []capability.Name{capability.NameAristaEOS},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
	},
}
