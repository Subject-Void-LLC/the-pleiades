// This file holds the Services section of docs/hephaestus.md's catalog:
// ansible.builtin.service, ansible.builtin.systemd, and
// ansible.windows.win_service. docs/hephaestus.md itself says method
// counts here are approximate until this phase generates them; svc.*'s own
// five verbs (start/stop/restart/enable/disable) are the base every
// service-manager-flavored entry mirrors, systemd additionally getting
// daemon_reload.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var servicesCollections = []collectionscaffold.Config{
	{
		Name:              "svc.start",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Starts a service using the target's own service manager, whichever it is."},
	},
	{
		Name:              "svc.stop",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Stops a service using the target's own service manager, whichever it is."},
	},
	{
		Name:              "svc.restart",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Restarts a service using the target's own service manager, whichever it is."},
	},
	{
		Name:              "svc.enable",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Enables a service to start at boot, using the target's own service manager."},
	},
	{
		Name:              "svc.disable",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Disables a service from starting at boot, using the target's own service manager."},
	},
	{
		Name:              "svc.systemd.start",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Starts a systemd unit."},
	},
	{
		Name:              "svc.systemd.stop",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Stops a systemd unit."},
	},
	{
		Name:              "svc.systemd.restart",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Restarts a systemd unit."},
	},
	{
		Name:              "svc.systemd.enable",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Enables a systemd unit to start at boot."},
	},
	{
		Name:              "svc.systemd.disable",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Disables a systemd unit from starting at boot."},
	},
	{
		Name:              "svc.systemd.daemon_reload",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Reloads systemd's unit files, after one on disk has changed."},
	},
	{
		Name:              "svc.windows.start",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Starts a Windows service."},
	},
	{
		Name:              "svc.windows.stop",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Stops a Windows service."},
	},
	{
		Name:              "svc.windows.restart",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Restarts a Windows service."},
	},
	{
		Name:              "svc.windows.enable",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Sets a Windows service's start type to automatic."},
	},
	{
		Name:              "svc.windows.disable",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc:               collection.Doc{Summary: "Sets a Windows service's start type to disabled."},
	},
}
