// Package playbook: the identity, archive, mount, wait, facts and firewall
// modules, and the network modules. Data only.
package playbook

// miscModules maps the remaining modules this platform has a method for.
var miscModules = []Entry{
	{
		Module:  "ansible.builtin.user",
		Aliases: []string{"user"},
		Selectors: []Selector{{Arg: "state", Absent: "present", Choices: []Choice{
			{Values: []string{"present"}, Call: &Call{FQCN: "identity.user.create", Class: ClassAsserted, Basis: "the account present with the given attributes"},
				Ignores: []string{"remove"}},
			{Values: []string{"absent"}, Call: &Call{FQCN: "identity.user.remove", Class: ClassAsserted, Basis: "the account absent"},
				Ignores: []string{"uid", "group", "shell", "home", "comment", "create_home", "system"}},
		}}},
		Args: []Arg{
			{Name: "name", Aliases: []string{"user"}, To: "name"},
			{Name: "state", Handling: ArgSelector},
			{Name: "uid", To: "uid"},
			{Name: "group", To: "group"},
			{Name: "shell", To: "shell"},
			{Name: "home", To: "home"},
			{Name: "comment", To: "comment"},
			{Name: "create_home", Aliases: []string{"createhome"}, To: "create_home"},
			{Name: "system", To: "system"},
			{Name: "remove", To: "remove"},
			{Name: "password", Handling: ArgBlock, Code: "template.secret", Reason: "a password hash is a secret and is not copied into a runbook"},
		},
	},
	{
		Module:  "ansible.builtin.group",
		Aliases: []string{"group"},
		Selectors: []Selector{{Arg: "state", Absent: "present", Choices: []Choice{
			{Values: []string{"present"}, Call: &Call{FQCN: "identity.group.create", Class: ClassAsserted, Basis: "the group present"}},
			{Values: []string{"absent"}, Call: &Call{FQCN: "identity.group.remove", Class: ClassAsserted, Basis: "the group absent"},
				Ignores: []string{"gid", "system"}},
		}}},
		Args: []Arg{
			{Name: "name", To: "name"},
			{Name: "state", Handling: ArgSelector},
			{Name: "gid", To: "gid"},
			{Name: "system", To: "system"},
		},
	},
	{
		Module:  "ansible.builtin.unarchive",
		Aliases: []string{"unarchive"},
		// Only an archive already on the device converts: by default
		// unarchive copies it from the controller first.
		Selectors: []Selector{{Arg: "remote_src", Absent: "false", Bool: true, Choices: []Choice{
			{Values: []string{"true"}, Call: &Call{FQCN: "archive.extract", Class: ClassAsserted, Basis: "the archive's content present at dest"}},
			{Values: []string{"false"}, Code: "module.manual", Reason: "the archive is on the controller; copy it to the device first, then extract it with remote_src: true"},
		}}},
		Args: []Arg{
			{Name: "src", To: "src"},
			{Name: "dest", To: "dest"},
			{Name: "creates", To: "creates"},
			{Name: "remote_src", Handling: ArgSelector},
		},
	},
	{
		Module:  "ansible.posix.mount",
		Aliases: []string{"mount"},
		// persist is written on every call: the native methods default it
		// to true, which only mounted and absent mean.
		Selectors: []Selector{{Arg: "state", Choices: []Choice{
			{Values: []string{"mounted"}, Call: &Call{FQCN: "fs.mount", Class: ClassAsserted, Basis: "the filesystem mounted and in fstab", Fixed: map[string]any{"persist": true}, Note: mountPointNote}},
			{Values: []string{"ephemeral"}, Call: &Call{FQCN: "fs.mount", Class: ClassAsserted, Basis: "the filesystem mounted, not in fstab", Fixed: map[string]any{"persist": false}, Note: mountPointNote}},
			{Values: []string{"unmounted"}, Call: &Call{FQCN: "fs.unmount", Class: ClassAsserted, Basis: "the filesystem unmounted, fstab kept", Fixed: map[string]any{"persist": false}},
				Ignores: []string{"src", "fstype", "opts"}},
			{Values: []string{"absent"}, Call: &Call{FQCN: "fs.unmount", Class: ClassAsserted, Basis: "the filesystem unmounted and out of fstab", Fixed: map[string]any{"persist": true},
				Note: "Ansible also removes the mount point directory; fs.unmount leaves it"},
				Ignores: []string{"src", "fstype", "opts"}},
			{Values: []string{"present"}, Code: "args.value", Reason: "no native method writes an fstab entry without mounting it"},
		}}},
		Args: []Arg{
			{Name: "path", Aliases: []string{"name"}, To: "path"},
			{Name: "state", Handling: ArgSelector},
			{Name: "src", To: "src"},
			{Name: "fstype", To: "fstype"},
			{Name: "opts", To: "opts"},
			{Name: "fstab", To: "fstab"},
		},
	},
	{
		Module:  "ansible.builtin.wait_for",
		Aliases: []string{"wait_for"},
		Default: &Call{FQCN: "pleiades.builtin.wait.port", Class: ClassObserve, Basis: "a wait reads and changes nothing"},
		Args: []Arg{
			{Name: "port", To: "port"},
			{Name: "host", To: "host"},
			{Name: "timeout", To: "timeout"},
			{Name: "delay", To: "delay"},
			{Name: "sleep", To: "sleep"},
			{Name: "state", To: "state"},
			{Name: "path", Handling: ArgBlock, Code: "args.unmapped", Reason: "a wait on a path is wait.path's; write the task with it"},
			{Name: "search_regex", Handling: ArgBlock, Code: "args.unmapped", Reason: "a wait on content is wait.search's; write the task with it"},
		},
	},
	{
		Module:  "ansible.builtin.setup",
		Aliases: []string{"setup", "ansible.builtin.gather_facts", "gather_facts"},
		Default: &Call{FQCN: "facts.gather", Class: ClassObserve, Basis: "gathering facts reads and changes nothing"},
		Args: []Arg{
			{Name: "filter", To: "filter"},
			{Name: "gather_subset", Handling: ArgDrop, Code: "args.ignored", Reason: "facts.gather reads one small fixed set of facts, so there is no subset to choose"},
		},
	},
	{
		Module:  "ansible.posix.firewalld",
		Aliases: []string{"firewalld"},
		Selectors: []Selector{{Arg: "state", Choices: []Choice{
			{Values: []string{"enabled"}, Call: &Call{FQCN: "fw.firewalld.allow", Class: ClassAsserted, Basis: "the service open"}},
			{Values: []string{"disabled"}, Call: &Call{FQCN: "fw.firewalld.deny", Class: ClassAsserted, Basis: "the service closed"}},
		}}},
		Adjust: firewalldTimes,
		Args: []Arg{
			{Name: "service", To: "service"},
			{Name: "zone", To: "zone"},
			{Name: "permanent", To: "permanent"},
			{Name: "immediate", To: "immediate"},
			{Name: "state", Handling: ArgSelector},
			{Name: "port", Handling: ArgBlock, Code: "args.value", Reason: "Ansible writes port and protocol as one text (8080/tcp); write them as fw.firewalld's port and protocol"},
		},
	},
	{
		Module:  "ansible.builtin.ping",
		Aliases: []string{"ping"},
		Default: &Call{FQCN: "net.ssh.ping", Class: ClassObserve, Basis: "a ping reads and changes nothing"},
	},
	{
		Module:  "ansible.netcommon.cli_command",
		Aliases: []string{"cli_command"},
		Default: &Call{FQCN: "net.cli.command", Class: ClassImperative, Basis: commandBasis},
		Args: []Arg{
			{Name: "command", To: "command"},
			{Name: "prompt", Handling: ArgBlock, Code: "args.unmapped", Reason: "no native method answers a device's prompt"},
			{Name: "answer", Handling: ArgBlock, Code: "args.unmapped", Reason: "no native method answers a device's prompt"},
			{Name: "sendonly", Handling: ArgBlock, Code: "args.unmapped", Reason: "net.cli.command waits for the device's prompt"},
			{Name: "check_all", Handling: ArgBlock, Code: "args.unmapped", Reason: "no native method answers a device's prompt"},
		},
		Returns: map[string]string{"stdout": "stdout"},
	},
	{
		Module:  "ansible.netcommon.cli_config",
		Aliases: []string{"cli_config"},
		Default: &Call{FQCN: "net.cli.config", Class: ClassImperative, Basis: "net.cli.config sends every line on every run",
			Note: "cli_config sends only the lines missing from the running configuration; net.cli.config sends every line and always reports a change"},
		Args: []Arg{{Name: "config", To: "config"}},
	},
	{
		Module:  "cisco.ios.ios_config",
		Aliases: []string{"ios_config"},
		Default: &Call{FQCN: "net.ios.config", Class: ClassImperative, Basis: "net.ios.config sends every line on every run and compares none of them with the running configuration",
			Note: "ios_config sends only the lines missing from the running configuration; net.ios.config sends every line and always reports a change"},
		Args: []Arg{
			{Name: "lines", Aliases: []string{"commands"}, To: "lines"},
			{Name: "backup", To: "backup", Note: backupNote},
			{Name: "save_when", Handling: ArgDrop, Code: "module.semantics", Reason: "the configuration is not saved; add a net.ios.save task after this one"},
			{Name: "backup_options", Handling: ArgBlock, Code: "args.unmapped", Reason: "net.ios.config records a backup as a stat, not a file"},
			{Name: "parents", Handling: ArgBlock, Code: "args.unmapped", Reason: "write the parent lines into lines in order"},
		},
	},
	{
		Module:  "cisco.ios.ios_facts",
		Aliases: []string{"ios_facts"},
		Default: &Call{FQCN: "net.ios.facts", Class: ClassObserve, Basis: "gathering facts reads and changes nothing"},
		Args:    []Arg{{Name: "gather_subset", To: "gather_subset"}},
	},
	{
		Module:  "ansible.netcommon.netconf_config",
		Aliases: []string{"netconf_config"},
		Default: &Call{FQCN: "net.netconf.config", Class: ClassAsserted, Basis: "a configuration document the device should hold",
			Fixed: map[string]any{"lock": "always"},
			Note:  "Ansible edits the candidate datastore and commits when the device offers one; net.netconf.config edits running, since its target cannot be set here yet"},
		Adjust: netconfLock,
		Args: []Arg{
			{Name: "content", Aliases: []string{"xml"}, To: "content"},
			{Name: "target", Aliases: []string{"datastore"}, Handling: ArgBlock, Code: "netconf.target", Reason: "net.netconf.config's target is also the engine's device selector"},
			{Name: "default_operation", To: "default_operation"},
			{Name: "error_option", To: "error_option"},
			{Name: "lock", To: "lock"},
			{Name: "commit", To: "commit"},
			{Name: "backup", To: "backup", Note: backupNote},
		},
	},
}

// mountPointNote is the difference between Ansible's mount and fs.mount
// when the mount point is missing.
const mountPointNote = "Ansible creates a missing mount point directory; fs.mount fails instead"

// backupNote is how a network method's backup differs from Ansible's.
const backupNote = "the backup is kept as the task's backup stat, not written to a file beside the playbook"
