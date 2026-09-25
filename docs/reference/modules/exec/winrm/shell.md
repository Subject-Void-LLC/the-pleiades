---
status: beta
---

# exec.winrm.shell

Runs a command on a Windows target through PowerShell, cmd.exe or no shell, over WinRM.

Runs a command on a Windows host over WinRM, naming how it runs. This is exec.shell's Windows counterpart, and it is a separate FQCN for the same reason svc.systemd.start is separate from svc.start: the concrete method names the platform it actually speaks to. The task picks one of three modes. powershell runs a script through powershell.exe with -NoProfile and -NonInteractive, sent UTF-16LE and base64 encoded as -EncodedCommand requires, and keeps a failing native program's own exit code rather than flattening it to 1. cmd runs a one-line script through cmd.exe, which is the only way to reach a cmd builtin such as dir, set or %ERRORLEVEL%. none runs a Windows command line, reaching the program it names exactly as written, so that program parses its own arguments and nothing else acts on them. The WinRM service starts every command through cmd.exe and cannot be told not to, so every command line is escaped until that cmd.exe passes it through unchanged, and the parser that acts on the text is always the one the task named. Every mode shares cmd.exe's limit of 8191 characters, counted after escaping. Values the command needs go in env, never into its text. A command cannot be inspected, so this reports changed every time it runs.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WinRMCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Check mode | Not supported: a check run names this task as unchecked, since a PowerShell or cmd script can change anything the account can reach, and what it changes is known only once it has run; this method has no creates or removes guard for a check to read |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `command` | `string` | yes | - | The script to run, or with shell none the command line: a program and its arguments, quoted the way that program expects. It is passed on verbatim, so every metacharacter the named shell understands is syntax and any runbook value interpolated into it is code. Pass values in env instead. |
| `shell` | `string` | yes | `powershell` | How the command runs: powershell, cmd or none. powershell and cmd name the interpreter that reads a script, and none runs a command line directly with no interpreter. Required rather than defaulted silently, because the three read the same text three different ways and a command written for one is not safe in another. Which programs run is the device's own setting (cmd_path, powershell_path). |
| `env` | `dict` | no | - | Values the command reads, set as environment variables before it starts. Each name becomes PLEIADES_ followed by the name, read as $env:PLEIADES_NAME in PowerShell or !PLEIADES_NAME! in cmd, so a value never becomes part of the command text and needs no escaping for either shell. In cmd the %PLEIADES_NAME% form is refused, because cmd.exe expands it before it parses the line. Values must be strings, numbers or booleans. The environment is visible to other processes on the device, so a secret does not belong here. |
| `timeout` | `int` | no | `60` | How many seconds to wait for the script to finish before giving up. This bounds the whole operation, including a device that accepts the connection and then never answers, which is what a host looks like after a script has reconfigured its own network. Raise it for an installer or an update run; the default is short because most work here is not. |
| `expect_disconnect` | `bool` | no | `false` | Declare that this script is expected to destroy the connection carrying it, as an address change or a reboot does. The task then waits for the device to answer WinRM again instead of failing, and reports result_known false, because the script's exit status and output went down with the connection and are not recoverable. A device that never comes back is still a failure. |
| `reconnect_timeout` | `int` | no | `300` | How many seconds to wait for the device to answer again after an expected disconnect. Only meaningful with expect_disconnect, and setting it without that is refused rather than silently ignored. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `stdout` | `string` | always | Everything the script wrote to standard output. |
| `stderr` | `string` | always | Everything the script wrote to standard error. PowerShell progress output is suppressed before the script runs, so this carries real errors rather than progress records. |
| `exit_code` | `int` | always | The command's exit status. Under powershell a failing native program's own code survives, and a failed cmdlet reports 1. A non-zero status fails the task. |
| `result_known` | `bool` | always | Whether this task actually saw the script finish. False only after an expected disconnect, where stdout, stderr and the exit status are all unavailable. Check this before trusting the other three: an unreceived result and a silent success are otherwise indistinguishable. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This platform cannot see what a script did, so it cannot say what would undo it. The same reasoning exec.command and exec.shell record: an arbitrary script's effect is opaque, and a guessed inverse is worse than none because a rollback would run it believing it was true. A task whose effect must be reversible should use a method that models the change it is making.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `exec.shell`
- `exec.command`

## Examples

Read a fact from a Windows host:

```yaml
- name: Report the OS caption
  exec.winrm.shell:
    shell: powershell
    command: (Get-CimInstance Win32_OperatingSystem).Caption
  register: os_caption
```

Use a cmd builtin:

```yaml
- name: Show the environment cmd sees
  exec.winrm.shell:
    shell: cmd
    command: set
```

Pass a value as data, not script text:

```yaml
- name: Create the deployment folder
  exec.winrm.shell:
    shell: powershell
    command: New-Item -ItemType Directory -Force -Path $env:PLEIADES_DIR
    env:
      DIR: C:\Deploy
```

Run a program with no shell:

```yaml
- name: Query the time service
  exec.winrm.shell:
    shell: none
    command: w32tm /query /status
```

