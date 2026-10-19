// Package playbook: the Windows modules, from ansible.windows and
// community.windows. Data only.
package playbook

// winModules maps the Windows modules this platform has a method for, and
// says why the rest need a person. ansible.windows.win_service is in
// pkgModules, beside the other service modules.
var winModules = []Entry{
	{
		Module:  "ansible.windows.win_optional_feature",
		Aliases: []string{"win_optional_feature"},
		Selectors: []Selector{{Arg: "state", Absent: "present", Choices: []Choice{
			{Values: []string{"present"}, Call: &Call{FQCN: "win.feature.install", Class: ClassAsserted, Basis: "the feature enabled",
				Note: "win.feature.install also enables the features it needs (DISM /all), which Ansible does only with include_parent: true"}},
			{Values: []string{"absent"}, Call: &Call{FQCN: "win.feature.remove", Class: ClassAsserted, Basis: "the feature disabled"}},
		}}},
		Args: []Arg{
			{Name: "name", To: "name", Handling: ArgUnroll},
			{Name: "state", Handling: ArgSelector},
			{Name: "include_parent", Handling: ArgDrop, Code: "args.ignored", Reason: "win.feature.install always enables the features a feature needs"},
			{Name: "source", Handling: ArgBlock, Code: "args.unmapped", Reason: "win.feature.install installs from the host's own store; a payload from another source is win.capability.install's source"},
		},
		Returns: map[string]string{"reboot_required": "reboot_required"},
	},
	{
		Module:  "ansible.windows.win_reboot",
		Aliases: []string{"win_reboot"},
		Default: &Call{FQCN: "win.reboot", Class: ClassImperative, Basis: "a restart is an action with no resulting state of its own"},
		Args: []Arg{
			{Name: "reboot_timeout", Aliases: []string{"reboot_timeout_sec"}, To: "reboot_timeout"},
			{Name: "pre_reboot_delay", Aliases: []string{"pre_reboot_delay_sec"}, To: "pre_reboot_delay"},
			{Name: "post_reboot_delay", Aliases: []string{"post_reboot_delay_sec"}, To: "post_reboot_delay"},
			{Name: "connect_timeout", Aliases: []string{"connect_timeout_sec"}, To: "connect_timeout"},
			{Name: "msg", To: "msg"},
			{Name: "test_command", Handling: ArgBlock, Code: "args.unmapped", Reason: "win.reboot waits for the host to let its login in again, not for a command to pass; run the command in the next task"},
			{Name: "boot_time_command", Handling: ArgBlock, Code: "args.unmapped", Reason: "win.reboot reads the boot time itself"},
		},
	},
	{
		Module:  "ansible.windows.win_get_url",
		Aliases: []string{"win_get_url"},
		// win.file.download holds every download to a SHA-256, which
		// win_get_url only checks when given one.
		Selectors: []Selector{{Arg: "checksum_algorithm", Absent: "sha1", Choices: []Choice{
			{Values: []string{"sha256"}, Call: &Call{FQCN: "win.file.download", Class: ClassAsserted, Basis: "the file present with its digest"}},
			{Values: []string{"md5", "sha1", "sha384", "sha512"}, Code: "args.value",
				Reason: "win.file.download checks a SHA-256 only; give checksum_algorithm: sha256 and the file's SHA-256 as checksum"},
		}}},
		Args: []Arg{
			{Name: "url", To: "url"},
			{Name: "dest", To: "path"},
			{Name: "checksum", To: "sha256"},
			{Name: "checksum_algorithm", Handling: ArgSelector},
			{Name: "force", Handling: ArgDrop, Code: "args.ignored", Reason: "win.file.download fetches the file again only when the one there does not have the digest"},
		},
	},
	{
		Module:  "community.windows.win_firewall",
		Aliases: []string{"win_firewall"},
		Default: &Call{FQCN: "win.firewall.profile", Class: ClassAsserted, Basis: "the profile's default actions as given"},
		Selectors: []Selector{{Arg: "state", Choices: []Choice{
			{Values: []string{"enabled"}, Call: &Call{FQCN: "win.firewall.profile", Class: ClassAsserted, Basis: "the profile on", Fixed: map[string]any{"enabled": true}}},
			{Values: []string{"disabled"}, Call: &Call{FQCN: "win.firewall.profile", Class: ClassAsserted, Basis: "the profile off", Fixed: map[string]any{"enabled": false}}},
		}}},
		Args: []Arg{
			{Name: "profiles", To: "name", Handling: ArgUnroll},
			{Name: "state", Handling: ArgSelector},
			{Name: "inbound_action", To: "default_inbound_action"},
			{Name: "outbound_action", To: "default_outbound_action"},
		},
	},
	{Module: "community.windows.win_firewall_rule", Aliases: []string{"win_firewall_rule"}, Rating: RatingManual, Code: "module.manual",
		Reason: "win_firewall_rule finds a rule by its display name, and win.firewall.rule by its Name, which for a rule Ansible made is a GUID; converted, the task would make a second rule beside it. Write win.firewall.rule with the rule's Name (Get-NetFirewallRule shows it)"},
	{Module: "ansible.windows.win_feature", Aliases: []string{"win_feature"}, Rating: RatingManual, Code: "module.manual",
		Reason: "win_feature takes Server Manager names (Web-Server) and win.feature.install DISM's (IIS-WebServerRole), which differ; write the task with the DISM name"},
	{Module: "community.windows.win_scheduled_task", Aliases: []string{"win_scheduled_task"}, Rating: RatingManual, Code: "module.manual",
		Reason: "win_scheduled_task takes lists of actions and triggers; win.schtask registers one of each, so a person chooses them"},
}
