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
			Description: "Runs a single command on the target over SSH and reports its exit status, stdout and stderr. No shell interprets the command: a semicolon, pipe or dollar sign in an argument is passed through as text, so this cannot chain commands, expand a variable or redirect output. Use exec.shell when those are what you want. A command cannot be inspected, so this reports changed every time it runs; creates and removes are how a task says what its work having already happened looks like, and a run they short-circuit reports no change.",
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
					RunbookYAML: "- name: Read the kernel version\n  fqcn: exec.command\n  params:\n    cmd: uname -r\n  register: kernel\n",
				},
				{
					Name:        "Make a command idempotent with creates",
					RunbookYAML: "- name: Unpack the release once\n  fqcn: exec.command\n  params:\n    cmd: tar -xzf /tmp/release.tgz\n    chdir: /opt/app\n    creates: /opt/app/VERSION\n",
				},
				{
					Name:        "Pass an argument that a shell would mangle",
					RunbookYAML: "- name: Write a literal value\n  fqcn: exec.command\n  params:\n    argv:\n      - /usr/bin/logger\n      - \"deployed $VERSION; done\"\n",
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
			Description: "Runs a command line on the target through a real shell, which is what makes a pipe, a redirect, a variable expansion, a glob or a chain of commands behave the way they would if you typed them. That is also the whole risk: every one of those characters is syntax, so any runbook value interpolated into this command is code. Use exec.command when the command is a single program with arguments, which is most of the time. A command cannot be inspected, so this reports changed every time it runs; creates and removes are how a task says what its work having already happened looks like.",
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
					RunbookYAML: "- name: Count the failed units\n  fqcn: exec.shell\n  params:\n    cmd: systemctl list-units --state=failed --no-legend | wc -l\n  register: failed\n",
				},
				{
					Name:        "Redirect output to a file, once",
					RunbookYAML: "- name: Snapshot the package list\n  fqcn: exec.shell\n  params:\n    cmd: dpkg -l > /var/backups/packages.txt\n    creates: /var/backups/packages.txt\n",
				},
				{
					Name:        "Choose the shell",
					RunbookYAML: "- name: Use a bash-only construct\n  fqcn: exec.shell\n  params:\n    cmd: \"[[ -f /etc/os-release ]] && echo present\"\n    executable: /bin/bash\n",
				},
			},
			SeeAlso: []string{"exec.command"},
		},
	},
}
