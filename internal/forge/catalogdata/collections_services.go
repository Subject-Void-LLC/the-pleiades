// This file holds the Services section of docs/hephaestus.md's catalog:
// ansible.builtin.service, ansible.builtin.systemd, and
// ansible.windows.win_service. docs/hephaestus.md itself says method
// counts here are approximate until this phase generates them; svc.*'s own
// five verbs (start/stop/restart/enable/disable) are the base every
// service-manager-flavored entry mirrors, systemd additionally getting
// daemon_reload.
package catalogdata

import (
	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

var servicesCollections = []collectionscaffold.Config{
	{
		Name:              "svc.start",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.stop",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.restart",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.enable",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.disable",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.systemd.start",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.systemd.stop",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.systemd.restart",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.systemd.enable",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.systemd.disable",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.systemd.daemon_reload",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.windows.start",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.windows.stop",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.windows.restart",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.windows.enable",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
	{
		Name:              "svc.windows.disable",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
	},
}
