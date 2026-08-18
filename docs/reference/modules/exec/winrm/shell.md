---
status: beta
---

# exec.winrm.shell

Runs a script on a Windows target through PowerShell or cmd.exe, over WinRM.

Runs a script on a Windows host over WinRM, naming which interpreter runs it. This is exec.shell's Windows counterpart, and it is a separate FQCN for the same reason svc.systemd.start is separate from svc.start: the concrete method names the platform it actually speaks to. Two shells are available and the task must pick one. powershell encodes the script UTF-16LE and base64 into powershell.exe -EncodedCommand, which is also what makes it safe: the base64 alphabet contains no cmd.exe metacharacter, so no script content can reach a shell as syntax. cmd runs the script through cmd.exe, which is the only way to reach a cmd builtin such as dir, set or %ERRORLEVEL%. Running a program directly with an argument vector nothing parses is refused rather than approximated, because the WS-Man option that would make it true is not settable from here. A script cannot be inspected, so this reports changed every time it runs.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WinRMCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `command` | `string` | yes | - | The script to run. It is passed to the interpreter named by shell, verbatim, so every metacharacter that interpreter understands is syntax and any runbook value interpolated into it is code. |
| `shell` | `string` | yes | `powershell` | Which interpreter runs the script: powershell or cmd. Required rather than defaulted silently, because the two have disjoint metacharacter sets and a script written for one is not safe in the other. The value none is refused: it would mean running a program directly with no interpreter, and this transport cannot promise that. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `stdout` | `string` | always | Everything the script wrote to standard output. |
| `stderr` | `string` | always | Everything the script wrote to standard error. PowerShell progress output is suppressed before the script runs, so this carries real errors rather than progress records. |
| `exit_code` | `int` | always | The script's exit status. A non-zero status fails the task. |

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

