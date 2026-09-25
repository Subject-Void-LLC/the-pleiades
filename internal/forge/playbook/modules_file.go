// Package playbook: the file modules. Data only.
package playbook

// fileAttrs are the ownership and mode arguments the file methods share.
var fileAttrs = []Arg{
	{Name: "mode", To: "mode"},
	{Name: "owner", To: "owner"},
	{Name: "group", To: "group"},
}

// fileModules maps file, copy, lineinfile and blockinfile.
var fileModules = []Entry{
	{
		Module:  "ansible.builtin.file",
		Aliases: []string{"file"},
		// src means something only to link and hard; absent ignores every
		// attribute.
		Selectors: []Selector{{Arg: "state", Absent: "file", Choices: []Choice{
			{Values: []string{"directory"}, Call: &Call{FQCN: "file.directory", Class: ClassAsserted, Basis: "the directory present"}, Ignores: []string{"src"}},
			{Values: []string{"touch"}, Call: &Call{FQCN: "file.touch", Class: ClassImperative, Basis: "touch updates the file's times on every run"}, Ignores: []string{"src"}},
			// Ansible's absent deletes a directory tree; file.remove does that
			// only when asked, so the call asks.
			{Values: []string{"absent"}, Call: &Call{FQCN: "file.remove", Class: ClassAsserted, Basis: "the path absent", Fixed: map[string]any{"recurse": true}},
				Ignores: []string{"src", "mode", "owner", "group"}},
			{Values: []string{"link"}, Call: &Call{FQCN: "file.symlink", Class: ClassAsserted, Basis: "the link present"}},
			{Values: []string{"file"}, Ignores: []string{"src"}, Call: &Call{FQCN: "file.permissions", Class: ClassAsserted, Basis: "the path's owner, group and mode",
				Note: "file.permissions refuses a symbolic link, which Ansible follows, and needs at least one of mode, owner and group"}},
			{Values: []string{"hard"}, Code: "args.value", Reason: "no native method makes a hard link"},
		}}},
		Args: append([]Arg{
			{Name: "path", Aliases: []string{"dest", "name"}, To: "path"},
			{Name: "state", Handling: ArgSelector},
			{Name: "src", To: "src"},
		}, fileAttrs...),
	},
	{
		Module:  "ansible.builtin.copy",
		Aliases: []string{"copy"},
		Default: &Call{FQCN: "file.copy", Class: ClassAsserted, Basis: "the file's content"},
		Adjust:  copyMode,
		Args: append([]Arg{
			{Name: "dest", To: "dest"},
			{Name: "content", To: "content"},
			{Name: "src", Handling: ArgBlock, Code: "args.unmapped", Reason: "file.copy writes content given in the task and never reads a file beside the playbook; put the content in the task"},
			{Name: "validate", Handling: ArgBlock, Code: "args.unmapped", Reason: "file.copy does not validate the new content before replacing the file"},
			{Name: "backup", Handling: ArgDrop, Code: "module.semantics", Reason: "no backup of the old file is kept"},
		}, fileAttrs...),
	},
	{
		Module:  "ansible.builtin.lineinfile",
		Aliases: []string{"lineinfile"},
		Selectors: []Selector{{Arg: "state", Absent: "present", Choices: []Choice{
			{Values: []string{"present"}, Call: &Call{FQCN: "file.line.set", Class: ClassAsserted, Basis: "the line present"}},
			{Values: []string{"absent"}, Call: &Call{FQCN: "file.line.remove", Class: ClassAsserted, Basis: "the line absent"}},
		}}},
		Args: []Arg{
			{Name: "path", Aliases: []string{"dest", "destfile", "name"}, To: "path"},
			{Name: "state", Handling: ArgSelector},
			{Name: "line", Aliases: []string{"value"}, To: "line"},
			{Name: "regexp", Aliases: []string{"regex"}, To: "regexp"},
			{Name: "insertafter", To: "insertafter"},
			{Name: "insertbefore", To: "insertbefore"},
		},
	},
	{
		Module:  "ansible.builtin.blockinfile",
		Aliases: []string{"blockinfile"},
		Selectors: []Selector{{Arg: "state", Absent: "present", Choices: []Choice{
			{Values: []string{"present"}, Call: &Call{FQCN: "file.block.set", Class: ClassAsserted, Basis: "the block present"}},
			{Values: []string{"absent"}, Call: &Call{FQCN: "file.block.remove", Class: ClassAsserted, Basis: "the block absent"}},
		}}},
		Args: []Arg{
			{Name: "path", Aliases: []string{"dest", "destfile", "name"}, To: "path"},
			{Name: "state", Handling: ArgSelector},
			{Name: "block", Aliases: []string{"content"}, To: "block"},
			{Name: "marker", To: "marker"},
			{Name: "marker_begin", To: "marker_begin"},
			{Name: "marker_end", To: "marker_end"},
		},
	},
}
