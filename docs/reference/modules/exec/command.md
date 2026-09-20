---
status: beta
---

# exec.command

Runs one command directly, with no shell involved.

Runs a single command on the target over SSH and reports its exit status, stdout and stderr. No shell interprets the command: a semicolon, pipe or dollar sign in an argument is passed through as text, so this cannot chain commands, expand a variable or redirect output. Use exec.shell when those are what you want. A command cannot be inspected, so this reports changed every time it runs; creates and removes are how a task says what its work having already happened looks like, and a run they short-circuit reports no change. Only a call with creates or removes can be checked: a check reads the guard's path and reports whether the command would run, running nothing. Any other call is named as unchecked, and check_mode on one is refused when the runbook is validated.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported for some calls, named in the description: those report what they would change and change nothing, a check run names any other as unchecked, and validation refuses check_mode on one |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `cmd` | `string` | no | - | The command and its arguments as one string, split the way a shell splits a command line: whitespace separates arguments, and single quotes, double quotes and backslashes group them. Mutually exclusive with argv. |
| `argv` | `list of string` | no | - | The command and its arguments already split, one element each. Preferred when an argument contains characters whose quoting would be awkward to write. Mutually exclusive with cmd. |
| `chdir` | `string` | no | - | Change into this directory before running, and resolve a relative creates or removes against it too. The command does not run at all if the directory does not exist. Defaults to the device's own working directory, or to wherever the account lands on login. |
| `creates` | `string` | no | - | A path whose existence on the target means this work is already done. When it exists, the command does not run and the task reports no change. A relative path is resolved from chdir, the same directory the command itself runs in. |
| `removes` | `string` | no | - | A path whose absence on the target means this work is already done. When it is missing, the command does not run and the task reports no change. A relative path is resolved from chdir, the same directory the command itself runs in. |
| `stdin` | `string` | no | - | Text piped to the command's standard input. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `rc` | `int` | always | The command's exit status. Zero when it succeeded, and 0 for a run that creates or removes skipped. |
| `stdout` | `string` | always | Everything the command wrote to standard output, with the trailing newline removed. |
| `stderr` | `string` | always | Everything the command wrote to standard error, with the trailing newline removed. |
| `cmd` | `string` | always | The exact command line sent to the device, empty for a skipped run. |
| `skipped` | `bool` | always | True when creates or removes short-circuited this task, so no command ran. |
| `msg` | `string` | on skip | Why the task was skipped. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

An arbitrary command's effect is unknown to this platform, so no undo can be derived from it. Pair the task with creates or removes to make re-running it safe, which is idempotence rather than rollback.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `exec.shell`

## Examples

Run a command and register its output:

```yaml
- name: Read the kernel version
  exec.command:
    cmd: uname -r
  register: kernel
```

Make a command idempotent with creates:

```yaml
- name: Unpack the release once
  exec.command:
    cmd: tar -xzf /tmp/release.tgz
    chdir: /opt/app
    creates: /opt/app/VERSION
```

Pass an argument that a shell would mangle:

```yaml
- name: Write a literal value
  exec.command:
    argv:
      - /usr/bin/logger
      - "deployed $VERSION; done"
```

