// This file holds the Execution section of docs/hephaestus.md's catalog:
// ansible.builtin.command and ansible.builtin.shell.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var execCollections = []collectionscaffold.Config{
	// exec.command is Phase 38's first write-capable method, and the one
	// that establishes what Changed means for the rest of the tier. Its
	// Doc is the full contract rather than a bare Summary because an
	// implemented method owes one; internal/archtest's
	// TestCatalogDataDocsMatchTheRegistry is what keeps this copy and the
	// registered manifest from drifting apart.
	{
		Name:          "exec.command",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs one command directly, with no shell involved.",
			Description: "Runs a single command on the target over SSH and reports its exit status, stdout and stderr. No shell interprets the command: a semicolon, pipe or dollar sign in an argument is passed through as text, so this cannot chain commands, expand a variable or redirect output. Use exec.shell when those are what you want. A command cannot be inspected, so this reports changed every time it runs; creates and removes are how a task says what its work having already happened looks like, and a run they short-circuit reports no change. Only a call with creates or removes can be checked: a check reads the guard's path and reports whether the command would run, running nothing. Any other call is named as unchecked, and check_mode on one is refused when the runbook is validated.",
			Params: []collection.Param{
				{Name: "cmd", Type: "string", Description: "The command and its arguments as one string, split the way a shell splits a command line: whitespace separates arguments, and single quotes, double quotes and backslashes group them. Mutually exclusive with argv."},
				{Name: "argv", Type: "list of string", Description: "The command and its arguments already split, one element each. Preferred when an argument contains characters whose quoting would be awkward to write. Mutually exclusive with cmd."},
				{Name: "chdir", Type: "string", Description: "Change into this directory before running, and resolve a relative creates or removes against it too. The command does not run at all if the directory does not exist. Defaults to the device's own working directory, or to wherever the account lands on login."},
				{Name: "creates", Type: "string", Description: "A path whose existence on the target means this work is already done. When it exists, the command does not run and the task reports no change. A relative path is resolved from chdir, the same directory the command itself runs in."},
				{Name: "removes", Type: "string", Description: "A path whose absence on the target means this work is already done. When it is missing, the command does not run and the task reports no change. A relative path is resolved from chdir, the same directory the command itself runs in."},
				{Name: "stdin", Type: "string", Description: "Text piped to the command's standard input."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "rc", Type: "int", Returned: "always", Description: "The command's exit status. Zero when it succeeded, and 0 for a run that creates or removes skipped."},
				{Name: "stdout", Type: "string", Returned: "always", Description: "Everything the command wrote to standard output, with the trailing newline removed."},
				{Name: "stderr", Type: "string", Returned: "always", Description: "Everything the command wrote to standard error, with the trailing newline removed."},
				{Name: "cmd", Type: "string", Returned: "always", Description: "The exact command line sent to the device, empty for a skipped run."},
				{Name: "skipped", Type: "bool", Returned: "always", Description: "True when creates or removes short-circuited this task, so no command ran."},
				{Name: "msg", Type: "string", Returned: "on skip", Description: "Why the task was skipped."},
			},
			Examples: []collection.Example{
				{
					Name:        "Run a command and register its output",
					RunbookYAML: "- name: Read the kernel version\n  exec.command:\n    cmd: uname -r\n  register: kernel\n",
				},
				{
					Name:        "Make a command idempotent with creates",
					RunbookYAML: "- name: Unpack the release once\n  exec.command:\n    cmd: tar -xzf /tmp/release.tgz\n    chdir: /opt/app\n    creates: /opt/app/VERSION\n",
				},
				{
					Name:        "Pass an argument that a shell would mangle",
					RunbookYAML: "- name: Write a literal value\n  exec.command:\n    argv:\n      - /usr/bin/logger\n      - \"deployed $VERSION; done\"\n",
				},
			},
			SeeAlso: []string{"exec.shell"},
		},
	},
	{
		Name:          "exec.shell",
		Capabilities:  []capability.Name{capability.NameShellExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a command through the target's shell, so pipes and redirects work.",
			Description: "Runs a command line on the target through a real shell, which is what makes a pipe, a redirect, a variable expansion, a glob or a chain of commands behave the way they would if you typed them. That is also the whole risk: every one of those characters is syntax, so any runbook value interpolated into this command is code. Use exec.command when the command is a single program with arguments, which is most of the time. A command cannot be inspected, so this reports changed every time it runs; creates and removes are how a task says what its work having already happened looks like. Only a call with creates or removes can be checked: a check reads the guard's path and reports whether the line would run, running nothing. Any other call is named as unchecked, and check_mode on one is refused when the runbook is validated.",
			Params: []collection.Param{
				{Name: "cmd", Type: "string", Required: true, Description: "The command line, passed to the shell exactly as written. Pipes, redirects, globs, variable expansions and semicolons all work, because the shell sees them."},
				{Name: "executable", Type: "string", Description: "The shell to run the command with, invoked as `<executable> -c <cmd>`. Defaults to the shell the device declares, or /bin/sh."},
				{Name: "chdir", Type: "string", Description: "Change into this directory before running, and resolve a relative creates or removes against it too. The command does not run at all if the directory does not exist. Defaults to the device's own working directory, or to wherever the account lands on login."},
				{Name: "creates", Type: "string", Description: "A path whose existence on the target means this work is already done. When it exists, the command does not run and the task reports no change. A relative path is resolved from chdir, the same directory the command itself runs in."},
				{Name: "removes", Type: "string", Description: "A path whose absence on the target means this work is already done. When it is missing, the command does not run and the task reports no change. A relative path is resolved from chdir, the same directory the command itself runs in."},
				{Name: "stdin", Type: "string", Description: "Text piped to the command's standard input."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "rc", Type: "int", Returned: "always", Description: "The command's exit status, which for a pipeline is the last command's. Zero when it succeeded, and 0 for a run that creates or removes skipped."},
				{Name: "stdout", Type: "string", Returned: "always", Description: "Everything the command wrote to standard output, with the trailing newline removed."},
				{Name: "stderr", Type: "string", Returned: "always", Description: "Everything the command wrote to standard error, with the trailing newline removed."},
				{Name: "cmd", Type: "string", Returned: "always", Description: "The command line sent to the device, empty for a skipped run."},
				{Name: "skipped", Type: "bool", Returned: "always", Description: "True when creates or removes short-circuited this task, so no command ran."},
				{Name: "msg", Type: "string", Returned: "on skip", Description: "Why the task was skipped."},
			},
			Examples: []collection.Example{
				{
					Name:        "Use a pipeline",
					RunbookYAML: "- name: Count the failed units\n  exec.shell:\n    cmd: systemctl list-units --state=failed --no-legend | wc -l\n  register: failed\n",
				},
				{
					Name:        "Redirect output to a file, once",
					RunbookYAML: "- name: Snapshot the package list\n  exec.shell:\n    cmd: dpkg -l > /var/backups/packages.txt\n    creates: /var/backups/packages.txt\n",
				},
				{
					Name:        "Choose the shell",
					RunbookYAML: "- name: Use a bash-only construct\n  exec.shell:\n    cmd: \"[[ -f /etc/os-release ]] && echo present\"\n    executable: /bin/bash\n",
				},
			},
			SeeAlso: []string{"exec.command"},
		},
	},
	{
		Name:          "exec.winrm.shell",
		Capabilities:  []capability.Name{capability.NameWinRM},
		Transports:    []string{"winrm"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a command on a Windows target through PowerShell, cmd.exe or no shell, over WinRM.",
			Description: "Runs a command on a Windows host over WinRM, naming how it runs. This is exec.shell's Windows counterpart, and it is a separate FQCN for the same reason svc.systemd.start is separate from svc.start: the concrete method names the platform it actually speaks to. The task picks one of three modes. powershell runs a script through powershell.exe with -NoProfile and -NonInteractive, sent UTF-16LE and base64 encoded as -EncodedCommand requires, and keeps a failing native program's own exit code rather than flattening it to 1. cmd runs a one-line script through cmd.exe, which is the only way to reach a cmd builtin such as dir, set or %ERRORLEVEL%. none runs a Windows command line, reaching the program it names exactly as written, so that program parses its own arguments and nothing else acts on them. The WinRM service starts every command through cmd.exe and cannot be told not to, so every command line is escaped until that cmd.exe passes it through unchanged, and the parser that acts on the text is always the one the task named. Every mode shares cmd.exe's limit of 8191 characters, counted after escaping. Values the command needs go in env, never into its text. A command cannot be inspected, so this reports changed every time it runs.",
			Params: []collection.Param{
				{Name: "command", Type: "string", Required: true, Description: "The script to run, or with shell none the command line: a program and its arguments, quoted the way that program expects. It is passed on verbatim, so every metacharacter the named shell understands is syntax and any runbook value interpolated into it is code. Pass values in env instead."},
				{Name: "shell", Type: "string", Required: true, Default: "powershell", Description: "How the command runs: powershell, cmd or none. powershell and cmd name the interpreter that reads a script, and none runs a command line directly with no interpreter. Required rather than defaulted silently, because the three read the same text three different ways and a command written for one is not safe in another. Which programs run is the device's own setting (cmd_path, powershell_path)."},
				{Name: "env", Type: "dict", Description: "Values the command reads, set as environment variables before it starts. Each name becomes PLEIADES_ followed by the name, read as $env:PLEIADES_NAME in PowerShell or !PLEIADES_NAME! in cmd, so a value never becomes part of the command text and needs no escaping for either shell. In cmd the %PLEIADES_NAME% form is refused, because cmd.exe expands it before it parses the line. Values must be strings, numbers or booleans. The environment is visible to other processes on the device, so a secret does not belong here."},
				{Name: "timeout", Type: "int", Default: "60", Description: "How many seconds to wait for the script to finish before giving up. This bounds the whole operation, including a device that accepts the connection and then never answers, which is what a host looks like after a script has reconfigured its own network. Raise it for an installer or an update run; the default is short because most work here is not."},
				{Name: "expect_disconnect", Type: "bool", Default: "false", Description: "Declare that this script is expected to destroy the connection carrying it, as an address change or a reboot does. The task then waits for the device to answer WinRM again instead of failing, and reports result_known false, because the script's exit status and output went down with the connection and are not recoverable. A device that never comes back is still a failure."},
				{Name: "reconnect_timeout", Type: "int", Default: "300", Description: "How many seconds to wait for the device to answer again after an expected disconnect. Only meaningful with expect_disconnect, and setting it without that is refused rather than silently ignored."},
			},
			Returns: []collection.ReturnField{
				{Name: "stdout", Type: "string", Returned: "always", Description: "Everything the script wrote to standard output."},
				{Name: "stderr", Type: "string", Returned: "always", Description: "Everything the script wrote to standard error. PowerShell progress output is suppressed before the script runs, so this carries real errors rather than progress records."},
				{Name: "exit_code", Type: "int", Returned: "always", Description: "The command's exit status. Under powershell a failing native program's own code survives, and a failed cmdlet reports 1. A non-zero status fails the task."},
				{Name: "result_known", Type: "bool", Returned: "always", Description: "Whether this task actually saw the script finish. False only after an expected disconnect, where stdout, stderr and the exit status are all unavailable. Check this before trusting the other three: an unreceived result and a silent success are otherwise indistinguishable."},
			},
			Examples: []collection.Example{
				{Name: "Read a fact from a Windows host", RunbookYAML: "- name: Report the OS caption\n  exec.winrm.shell:\n    shell: powershell\n    command: (Get-CimInstance Win32_OperatingSystem).Caption\n  register: os_caption\n"},
				{Name: "Use a cmd builtin", RunbookYAML: "- name: Show the environment cmd sees\n  exec.winrm.shell:\n    shell: cmd\n    command: set\n"},
				{Name: "Pass a value as data, not script text", RunbookYAML: "- name: Create the deployment folder\n  exec.winrm.shell:\n    shell: powershell\n    command: New-Item -ItemType Directory -Force -Path $env:PLEIADES_DIR\n    env:\n      DIR: C:\\Deploy\n"},
				{Name: "Run a program with no shell", RunbookYAML: "- name: Query the time service\n  exec.winrm.shell:\n    shell: none\n    command: w32tm /query /status\n"},
			},
			SeeAlso: []string{"exec.shell", "exec.command"},
		},
	},
}
