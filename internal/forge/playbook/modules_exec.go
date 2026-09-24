// Package playbook: the command-running modules. Data only.
package playbook

// commandBasis is why a command is imperative: running it is the only
// way to learn what it does, with or without a creates/removes guard,
// which makes it skippable but does not make it a desired state.
const commandBasis = "a command has no desired state; its effect is known only by running it"

// commandReturns are the result fields command and shell share with
// exec.command and exec.shell.
var commandReturns = map[string]string{"rc": "rc", "stdout": "stdout", "stderr": "stderr", "cmd": "cmd"}

// execModules maps command, shell and their free-form arguments.
var execModules = []Entry{
	{
		Module:   "ansible.builtin.command",
		Aliases:  []string{"command"},
		FreeForm: FreeFormCommand,
		RawKeys:  []string{"creates", "removes", "chdir", "stdin", "stdin_add_newline", "strip_empty_ends"},
		RawArg:   "cmd",
		Default:  &Call{FQCN: "exec.command", Class: ClassImperative, Basis: commandBasis},
		Args: []Arg{
			{Name: "cmd", Aliases: []string{"_raw_params"}, To: "cmd"},
			{Name: "argv", To: "argv"},
			{Name: "chdir", To: "chdir"},
			{Name: "creates", To: "creates"},
			{Name: "removes", To: "removes"},
			{Name: "stdin", To: "stdin"},
			{Name: "stdin_add_newline", Handling: ArgBlock, Code: "args.unmapped", Reason: "exec.command passes stdin exactly as given"},
			{Name: "strip_empty_ends", Handling: ArgBlock, Code: "args.unmapped", Reason: "exec.command reports stdout exactly as the command wrote it"},
			{Name: "expand_argument_vars", Handling: ArgBlock, Code: "args.unmapped", Reason: "exec.command never expands variables in its arguments"},
			{Name: "warn", Handling: ArgDrop, Code: "keyword.reporting", Reason: "it only controlled a warning"},
		},
		Returns: commandReturns,
	},
	{
		Module:   "ansible.builtin.shell",
		Aliases:  []string{"shell"},
		FreeForm: FreeFormCommand,
		RawKeys:  []string{"creates", "removes", "chdir", "executable", "stdin", "stdin_add_newline"},
		RawArg:   "cmd",
		Default:  &Call{FQCN: "exec.shell", Class: ClassImperative, Basis: commandBasis},
		Args: []Arg{
			{Name: "cmd", Aliases: []string{"_raw_params"}, To: "cmd"},
			{Name: "chdir", To: "chdir"},
			{Name: "creates", To: "creates"},
			{Name: "removes", To: "removes"},
			{Name: "executable", To: "executable"},
			{Name: "stdin", To: "stdin"},
			{Name: "stdin_add_newline", Handling: ArgBlock, Code: "args.unmapped", Reason: "exec.shell passes stdin exactly as given"},
			{Name: "warn", Handling: ArgDrop, Code: "keyword.reporting", Reason: "it only controlled a warning"},
		},
		Returns: commandReturns,
	},
	{
		Module:   "ansible.builtin.raw",
		Aliases:  []string{"raw"},
		FreeForm: FreeFormCommand,
		RawKeys:  []string{"executable"},
		RawArg:   "cmd",
		Default: &Call{FQCN: "exec.shell", Class: ClassImperative, Basis: commandBasis,
			Note: "raw runs the text through the account's login shell; exec.shell uses the shell the device declares, or /bin/sh, unless executable is given"},
		Args: []Arg{
			{Name: "cmd", Aliases: []string{"_raw_params"}, To: "cmd"},
			{Name: "executable", To: "executable"},
		},
		Returns: map[string]string{"rc": "rc", "stdout": "stdout", "stderr": "stderr"},
	},
}

// manualModules never convert mechanically: each needs a person, for the
// reason given.
var manualModules = []Entry{
	{Module: "ansible.builtin.template", Aliases: []string{"template"}, Rating: RatingManual, Code: "module.manual",
		Reason: "the platform's renderer has no {% %} statements and file.template is not implemented, so a template stays a manual step"},
	{Module: "ansible.builtin.assert", Aliases: []string{"assert"}, Rating: RatingManual, Code: "module.manual",
		Reason: "no native method stops a run on a condition; express it as when on the tasks that must not run"},
	{Module: "ansible.builtin.fail", Aliases: []string{"fail"}, Rating: RatingManual, Code: "module.manual",
		Reason: "no native method fails a run on purpose"},
	{Module: "ansible.builtin.pause", Aliases: []string{"pause"}, Rating: RatingManual, Code: "module.manual",
		Reason: "no native method waits for a person or a timer"},
	{Module: "ansible.builtin.script", Aliases: []string{"script"}, Rating: RatingManual, Code: "module.manual",
		Reason: "copying a local script to the device and running it is two native steps a person should write"},
	{Module: "ansible.builtin.add_host", Aliases: []string{"add_host"}, Rating: RatingManual, Code: "module.manual",
		Reason: "the inventory is not changed by a run"},
	{Module: "ansible.builtin.group_by", Aliases: []string{"group_by"}, Rating: RatingManual, Code: "module.manual",
		Reason: "the inventory is not changed by a run; tag the devices instead"},
	{Module: "ansible.builtin.uri", Aliases: []string{"uri"}, Rating: RatingManual, Code: "module.manual",
		Reason: "uri calls the URL from the device, and http.request calls it from wherever the task runs; which side should call it is a person's decision"},
	{Module: "ansible.builtin.get_url", Aliases: []string{"get_url"}, Rating: RatingManual, Code: "module.manual",
		Reason: "no native method downloads a file onto the device"},
	{Module: "ansible.builtin.stat", Aliases: []string{"stat"}, Rating: RatingManual, Code: "module.manual",
		Reason: "no native method registers a path's attributes; a task that waits for a path is wait.path's, and one that runs only when a path is missing can use creates"},
}
