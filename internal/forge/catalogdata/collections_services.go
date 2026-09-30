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
		Transports:        []string{"ssh", "winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Starts a service now, whichever service manager the device runs.",
			Description: "Makes sure a service is running right now, without caring which init system the device uses. This is ansible.builtin.service with state=started: it resolves the device's service manager and hands the call to that manager's concrete method, so on a Linux host it runs svc.systemd.start and behaves exactly as that method does, including reporting no change when the service is already running. Use the concrete method instead when a runbook is written for one platform and should say so.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on, as recorded by the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers."},
			},
			Examples: []collection.Example{
				{Name: "Start a service without naming the init system", RunbookYAML: "- name: Make sure nginx is running\n  svc.start:\n    name: nginx\n"},
			},
			SeeAlso: []string{"svc.stop", "svc.restart", "svc.enable", "svc.systemd.start"},
		},
	},
	{
		Name:              "svc.stop",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh", "winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a service now, whichever service manager the device runs.",
			Description: "Makes sure a service is not running right now, without caring which init system the device uses. This is ansible.builtin.service with state=stopped: it resolves the device's service manager and hands the call to that manager's concrete method. It stops the service now and leaves the boot-time setting alone, so a service stopped by this task starts again at the next reboot unless svc.disable is also run.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on, as recorded by the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers."},
			},
			Examples: []collection.Example{
				{Name: "Stop a service without naming the init system", RunbookYAML: "- name: Stop nginx before maintenance\n  svc.stop:\n    name: nginx\n"},
			},
			SeeAlso: []string{"svc.start", "svc.disable", "svc.systemd.stop"},
		},
	},
	{
		Name:              "svc.restart",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh", "winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Restarts a service, whichever service manager the device runs.",
			Description: "Restarts a service, starting it if it was not running. This is ansible.builtin.service with state=restarted: it resolves the device's service manager and hands the call to that manager's concrete method. It is the one method here that is never converged, since restarting a running service is the point rather than a no-op, so it always reports changed. That makes it the one most worth putting behind a when condition, so a service is only bounced when something it reads actually changed.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on, as recorded by the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers."},
			},
			Examples: []collection.Example{
				{Name: "Restart only when the config changed", RunbookYAML: "- name: Write the config\n  file.copy:\n    src: ./app.conf\n    dest: /etc/app/app.conf\n  register: app_config\n\n- name: Restart the service if the config changed\n  svc.restart:\n    name: app\n  when:\n    - app_config.changed\n"},
			},
			SeeAlso: []string{"svc.start", "svc.stop", "svc.systemd.restart"},
		},
	},
	{
		Name:              "svc.enable",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh", "winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes a service start at boot, whichever service manager the device runs.",
			Description: "Makes sure a service is set to start at boot, without caring which init system the device uses. This is ansible.builtin.service with enabled=yes: it resolves the device's service manager and hands the call to that manager's concrete method. It changes the boot-time setting only, so a service enabled by this task is not running until svc.start runs or the device reboots.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on, as recorded by the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers."},
			},
			Examples: []collection.Example{
				{Name: "Enable and start, in that order", RunbookYAML: "- name: Make sure the service comes back after a reboot\n  svc.enable:\n    name: app\n\n- name: And make sure it is running now\n  svc.start:\n    name: app\n"},
			},
			SeeAlso: []string{"svc.disable", "svc.start", "svc.systemd.enable"},
		},
	},
	{
		Name:              "svc.disable",
		Capabilities:      []capability.Name{capability.NameServiceManager},
		Transports:        []string{"ssh", "winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a service starting at boot, whichever service manager the device runs.",
			Description: "Makes sure a service is not set to start at boot, without caring which init system the device uses. This is ansible.builtin.service with enabled=no: it resolves the device's service manager and hands the call to that manager's concrete method. It changes the boot-time setting only, so a service disabled by this task keeps running until svc.stop runs or the device reboots.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The service to act on. This is passed straight through to the concrete method for the device's service manager, so it means whatever that manager means by a service name: a systemd unit such as nginx or nginx.service on a Linux host."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on, as recorded by the concrete method that ran."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the service manager reported before this task and after it. The exact keys come from the concrete method that ran, since what there is to say about a service differs between service managers."},
			},
			Examples: []collection.Example{
				{Name: "Take a service out of the boot sequence", RunbookYAML: "- name: Stop the service coming back after a reboot\n  svc.disable:\n    name: legacy-app\n"},
			},
			SeeAlso: []string{"svc.enable", "svc.stop", "svc.systemd.disable"},
		},
	},
	{
		Name:              "svc.systemd.start",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Starts a systemd unit now, without changing whether it starts at boot.",
			Description: "Makes sure a systemd unit is running right now. This is ansible.builtin.systemd with state=started, and it is deliberately not also enable: starting and enabling are separate in systemd and separate here, so a task that wants both says both. State is read before anything is sent, so a unit that is already running reports no change and no command reaches the device. A unit systemd does not know is refused rather than reported as started, and a masked unit is refused with the mask named, since systemd's own error for that case describes a symlink rather than the cause.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The unit this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Start a service", RunbookYAML: "- name: Make sure nginx is running\n  svc.systemd.start:\n    name: nginx\n"},
				{Name: "Start it and make it survive a reboot", RunbookYAML: "- name: Start nginx\n  svc.systemd.start:\n    name: nginx\n\n- name: Make nginx start at boot too\n  svc.systemd.enable:\n    name: nginx\n"},
			},
			SeeAlso: []string{"svc.systemd.stop", "svc.systemd.restart", "svc.systemd.enable", "svc.start"},
		},
	},
	{
		Name:              "svc.systemd.stop",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a systemd unit now, without changing whether it starts at boot.",
			Description: "Makes sure a systemd unit is not running right now. This is ansible.builtin.systemd with state=stopped, and it leaves the boot-time setting alone: a unit stopped by this task still starts at the next reboot unless svc.systemd.disable is also run. State is read before anything is sent, so a unit that is already stopped reports no change. A unit systemd does not know is refused rather than reported as stopped, which matters more here than anywhere else in this namespace: systemctl answers \"inactive\" for a name that has never existed, so a method trusting it would report success for a typo.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The unit this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Stop a service", RunbookYAML: "- name: Stop nginx before swapping its config\n  svc.systemd.stop:\n    name: nginx\n"},
				{Name: "Stop it now and keep it from coming back at boot", RunbookYAML: "- name: Stop nginx\n  svc.systemd.stop:\n    name: nginx\n\n- name: Keep nginx from starting at boot\n  svc.systemd.disable:\n    name: nginx\n"},
			},
			SeeAlso: []string{"svc.systemd.start", "svc.systemd.disable", "svc.stop"},
		},
	},
	{
		Name:              "svc.systemd.restart",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Restarts a systemd unit, starting it if it was not running.",
			Description: "Restarts a systemd unit. This is ansible.builtin.systemd with state=restarted, and like that module it starts a unit that was not running rather than failing. It is the one method in this namespace that is never converged: restarting a running unit is the point, not a no-op, so this always sends the command and always reports changed. That makes it the method most worth putting behind a when condition or a handler, so a service is only bounced when something it reads actually changed. A unit systemd does not know is refused, and a masked unit is refused with the mask named.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The unit this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Restart after a config change", RunbookYAML: "- name: Write the nginx config\n  file.copy:\n    src: ./nginx.conf\n    dest: /etc/nginx/nginx.conf\n  register: nginx_config\n\n- name: Restart nginx only if the config actually changed\n  svc.systemd.restart:\n    name: nginx\n  when:\n    - nginx_config.changed\n"},
			},
			SeeAlso: []string{"svc.systemd.start", "svc.systemd.stop", "svc.restart"},
		},
	},
	{
		Name:              "svc.systemd.enable",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes a systemd unit start at boot, without starting it now.",
			Description: "Makes sure a systemd unit is set to start at boot. This is ansible.builtin.systemd with enabled=yes, and it is deliberately not also start: a unit enabled by this task is not running until svc.systemd.start runs or the device reboots. State is read before anything is sent, so a unit that is already enabled reports no change. A static unit is refused with that word named, because systemd's own failure for enabling one describes a missing symlink rather than the cause, which is that the unit has no [Install] section and is meant to be pulled in by another unit.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The unit this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Make a service start at boot", RunbookYAML: "- name: Make sure nginx comes back after a reboot\n  svc.systemd.enable:\n    name: nginx\n"},
			},
			SeeAlso: []string{"svc.systemd.disable", "svc.systemd.start", "svc.enable"},
		},
	},
	{
		Name:              "svc.systemd.disable",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a systemd unit starting at boot, without stopping it now.",
			Description: "Makes sure a systemd unit is not set to start at boot. This is ansible.builtin.systemd with enabled=no, and it leaves the running system alone: a unit disabled by this task keeps running until svc.systemd.stop runs or the device reboots. State is read before anything is sent, so a unit that is already disabled reports no change. A static unit is refused, since a unit with no [Install] section was never enabled and cannot be disabled.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The systemd unit to act on, such as nginx or nginx.service. This is ansible.builtin.systemd's own parameter name. The unit must already exist: a name systemd does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The unit this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What systemd reported about the unit before this task and after it, each holding exists, active, enabled and systemd's own load_state, active_state and unit_file_state. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Keep a service from starting at boot", RunbookYAML: "- name: Stop nginx coming back after a reboot\n  svc.systemd.disable:\n    name: nginx\n"},
			},
			SeeAlso: []string{"svc.systemd.enable", "svc.systemd.stop", "svc.disable"},
		},
	},
	{
		Name:              "svc.systemd.daemon_reload",
		Capabilities:      []capability.Name{capability.NameSystemd},
		Transports:        []string{"ssh"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes systemd re-read every unit file on disk.",
			Description: "Runs systemctl daemon-reload, which makes systemd pick up unit files that were written, changed or removed since it last read them. This is ansible.builtin.systemd with daemon_reload=yes on its own. It is the step that makes a unit file an earlier task wrote visible to systemd at all: without it, svc.systemd.start on a brand new unit fails with the unit not found, which reads as a broken unit file rather than as a stale view. It takes no unit name, because it is not about one unit, and it always reports changed: systemd exposes no way to ask whether a reload would have made any difference, so claiming otherwise would be a guess.",
			Params: []collection.Param{
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Examples: []collection.Example{
				{Name: "Install a unit file and make systemd see it", RunbookYAML: "- name: Write the unit file\n  file.copy:\n    src: ./app.service\n    dest: /etc/systemd/system/app.service\n    mode: \"0644\"\n\n- name: Make systemd re-read its unit files\n  svc.systemd.daemon_reload: {}\n\n- name: Start the new service\n  svc.systemd.start:\n    name: app\n"},
			},
			SeeAlso: []string{"svc.systemd.start", "svc.systemd.enable"},
		},
	},
	{
		Name:              "svc.windows.start",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Starts a Windows service now, without changing its start type.",
			Description: "Makes sure a Windows service is running right now. This is ansible.windows.win_service with state=started, and it is deliberately not also enable: starting and setting the start type are separate in the Service Control Manager and separate here, so a task that wants both says both. State is read before anything is sent, so a service that is already running reports no change and no command reaches the device. A service the Service Control Manager does not know is refused rather than reported as started, and a service whose start type is Disabled is refused with that named, since Windows itself refuses to start one and its own error describes a generic failure rather than the cause.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Windows service to act on, its short service name (not its display name), such as Spooler rather than \"Print Spooler\". The service must already exist: a name the Service Control Manager does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the Service Control Manager reported about the service before this task and after it, each holding exists, running, status and start_type. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Start a service", RunbookYAML: "- name: Make sure the print spooler is running\n  svc.windows.start:\n    name: Spooler\n"},
				{Name: "Start it and make it survive a reboot", RunbookYAML: "- name: Start the print spooler\n  svc.windows.start:\n    name: Spooler\n\n- name: Make the print spooler start at boot too\n  svc.windows.enable:\n    name: Spooler\n"},
			},
			SeeAlso: []string{"svc.windows.stop", "svc.windows.restart", "svc.windows.enable", "svc.start"},
		},
	},
	{
		Name:              "svc.windows.stop",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a Windows service now, without changing its start type.",
			Description: "Makes sure a Windows service is not running right now. This is ansible.windows.win_service with state=stopped, and it leaves the start type alone: a service stopped by this task still starts at the next reboot unless svc.windows.disable is also run. State is read before anything is sent, so a service that is already stopped reports no change. A service the Service Control Manager does not know is refused rather than reported as stopped, since a typo in the name should not read as success.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Windows service to act on, its short service name (not its display name), such as Spooler rather than \"Print Spooler\". The service must already exist: a name the Service Control Manager does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the Service Control Manager reported about the service before this task and after it, each holding exists, running, status and start_type. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Stop a service", RunbookYAML: "- name: Stop the print spooler before changing its config\n  svc.windows.stop:\n    name: Spooler\n"},
				{Name: "Stop it now and keep it from coming back at boot", RunbookYAML: "- name: Stop the print spooler\n  svc.windows.stop:\n    name: Spooler\n\n- name: Keep the print spooler from starting at boot\n  svc.windows.disable:\n    name: Spooler\n"},
			},
			SeeAlso: []string{"svc.windows.start", "svc.windows.disable", "svc.stop"},
		},
	},
	{
		Name:              "svc.windows.restart",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Restarts a Windows service, starting it if it was not running.",
			Description: "Restarts a Windows service. This is ansible.windows.win_service with state=restarted, and like that module it starts a service that was not running rather than failing. It is the one method in this namespace that is never converged: restarting a running service is the point, not a no-op, so this always sends the command and always reports changed. That makes it the method most worth putting behind a when condition or a handler, so a service is only bounced when something it reads actually changed. A service the Service Control Manager does not know is refused, and a service whose start type is Disabled is refused with that named.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Windows service to act on, its short service name (not its display name), such as Spooler rather than \"Print Spooler\". The service must already exist: a name the Service Control Manager does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the Service Control Manager reported about the service before this task and after it, each holding exists, running, status and start_type. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Restart after a config change", RunbookYAML: "- name: Write the app's config\n  file.copy:\n    src: ./app.config\n    dest: C:\\Program Files\\App\\app.config\n  register: app_config\n\n- name: Restart the app service only if the config actually changed\n  svc.windows.restart:\n    name: AppService\n  when:\n    - app_config.changed\n"},
			},
			SeeAlso: []string{"svc.windows.start", "svc.windows.stop", "svc.restart"},
		},
	},
	{
		Name:              "svc.windows.enable",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Makes a Windows service start at boot, without starting it now.",
			Description: "Makes sure a Windows service's start type is Automatic. This is ansible.windows.win_service with start_mode=auto, and it is deliberately not also start: a service enabled by this task is not running until svc.windows.start runs or the device reboots. State is read before anything is sent, so a service whose start type is already Automatic reports no change. Windows recognizes a third start type, Manual, that neither this method nor svc.windows.disable targets: a service found Manual becomes Automatic, and running svc.windows.disable afterward would leave it Disabled rather than back at Manual, which is why that specific transition records no inverse instruction rather than a wrong one.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Windows service to act on, its short service name (not its display name), such as Spooler rather than \"Print Spooler\". The service must already exist: a name the Service Control Manager does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the Service Control Manager reported about the service before this task and after it, each holding exists, running, status and start_type. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Make a service start at boot", RunbookYAML: "- name: Make sure the print spooler comes back after a reboot\n  svc.windows.enable:\n    name: Spooler\n"},
			},
			SeeAlso: []string{"svc.windows.disable", "svc.windows.start", "svc.enable"},
		},
	},
	{
		Name:              "svc.windows.disable",
		Capabilities:      []capability.Name{capability.NameWindowsService},
		Transports:        []string{"winrm"},
		RequiresElevation: true,
		EngineVersion:     engineVersion,
		Doc: collection.Doc{
			Summary:     "Stops a Windows service starting at boot, without stopping it now.",
			Description: "Makes sure a Windows service's start type is Disabled, refusing it from starting at all until this is undone. This is ansible.windows.win_service with start_mode=disabled, and it leaves the running system alone: a service disabled by this task keeps running until svc.windows.stop also runs or it is stopped some other way. State is read before anything is sent, so a service whose start type is already Disabled reports no change. Windows recognizes a third start type, Manual, that neither this method nor svc.windows.enable targets: a service found Manual becomes Disabled, and running svc.windows.enable afterward would leave it Automatic rather than back at Manual, which is why that specific transition records no inverse instruction rather than a wrong one.",
			Params: []collection.Param{
				{Name: "name", Type: "string", Required: true, Description: "The Windows service to act on, its short service name (not its display name), such as Spooler rather than \"Print Spooler\". The service must already exist: a name the Service Control Manager does not know is refused rather than reported as already stopped, since that is nearly always a typo."},
			},
			Returns: []collection.ReturnField{
				{Name: "name", Type: "string", Returned: "always", Description: "The service this task acted on."},
				{Name: "diff", Type: "dict", Returned: "always", Description: "What the Service Control Manager reported about the service before this task and after it, each holding exists, running, status and start_type. Recorded even on a run that changed nothing, because \"it was already like this\" is what tells a later rollback to do nothing."},
			},
			Examples: []collection.Example{
				{Name: "Keep a service from starting at boot", RunbookYAML: "- name: Make sure the print spooler cannot start at boot\n  svc.windows.disable:\n    name: Spooler\n"},
				{Name: "Disable it and stop it running now too", RunbookYAML: "- name: Stop the print spooler\n  svc.windows.stop:\n    name: Spooler\n\n- name: Keep the print spooler from starting at boot\n  svc.windows.disable:\n    name: Spooler\n"},
			},
			SeeAlso: []string{"svc.windows.enable", "svc.windows.stop", "svc.disable"},
		},
	},
}
