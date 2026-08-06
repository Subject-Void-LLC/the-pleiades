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
	// The net.catalyst.* namespace is controller-side and read-only: each
	// method addresses a Cisco Catalyst Center over its REST API and
	// gathers facts about the fleet it manages, changing nothing. They are
	// the first methods in this catalog to reach status implemented, having
	// been verified against Cisco's public DevNet sandbox.
	{
		Name:          "net.catalyst.device_facts",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.catalyst.site_facts",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.catalyst.tag_facts",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
	},
	{
		Name:          "net.catalyst.reachability",
		Capabilities:  []capability.Name{capability.NameCatalystAPI},
		Transports:    []string{"https"},
		EngineVersion: engineVersion,
	},
}
