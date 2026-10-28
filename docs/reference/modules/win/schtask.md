---
status: beta
---

# win.schtask

Registers, updates or removes a Windows scheduled task over WinRM, or reports one as it is.

Reads the task first (Get-ScheduledTask, its XML definition and its last and next run), then: with state present, registers it with one action, one trigger and its principal when it is missing or differs in any of them, its description or whether it is enabled, and is no change when it matches; with absent, removes it; with query, only reports it. The script is a constant and every value reaches it as JSON on standard input, so a value can never be PowerShell syntax; every name is held to a strict pattern first. run_as SYSTEM, LOCAL SERVICE or NETWORK SERVICE runs as that service account; another account runs with S4U logon, so no password is stored with the task, and the task cannot reach the network as that user. A task this run registered undoes to its removal; one it changed or removed undoes to registering its earlier XML definition, withheld from the journal (xml, which also registers a task from a definition you give). A check reads and reports what would change.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WinRMCapable` |
| Transports | `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The task's path: \Folder\Name, or Name in the root folder. |
| `state` | `string` | no | `present` | present registers or updates it; absent removes it; query only reports it. **Choices:** `present`, `absent`, `query`. |
| `execute` | `string` | no | - | The program the task runs. Required with state present, unless xml is given. |
| `arguments` | `string` | no | - | Its arguments, as one string. |
| `working_directory` | `string` | no | - | The directory it starts in. |
| `trigger` | `string` | no | - | When it runs: at_startup, at_logon, daily (at, HH:MM, the device's local time) or once (at, an RFC 3339 time). Required with state present. **Choices:** `at_startup`, `at_logon`, `daily`, `once`. |
| `at` | `string` | no | - | The time of a daily or once trigger. |
| `run_as` | `string` | no | `SYSTEM` | The account it runs as: SYSTEM, LOCAL SERVICE, NETWORK SERVICE, or an account (DOMAIN\name or name) run with S4U logon and no stored password. |
| `run_level` | `string` | no | `limited` | Whether it runs with the account's highest privileges. **Choices:** `limited`, `highest`. |
| `enabled` | `bool` | no | `true` | Whether the task is enabled. |
| `description` | `string` | no | - | The task's description. |
| `xml` | `string` | no | - | A whole task definition, as Export-ScheduledTask writes it, registered as given with state present; what this method's own undo uses. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `name` | `string` | always | The task's full path. |
| `existed` | `bool` | always | Whether the task existed before. |
| `state` | `string` | always | Its state before (Ready, Running, Disabled), empty when it did not exist. |
| `last_run_time` | `string` | always | When it last ran. |
| `last_task_result` | `string` | always | Its last result code. |
| `next_run_time` | `string` | always | When it runs next. |
| `differences` | `list of string` | always | How it differed from what was asked, empty when it matched. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A task this run registered is removed by its undo. A task it changed or removed is registered again from the XML definition it had, read before the change; that XML is withheld from the journal, so a rollback replays it only from the task's own rollback: list. The task's run history is not restored.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `exec.winrm.shell`
- `svc.windows.start`

## Examples

A nightly cleanup:

```yaml
- name: Cleanup
  win.schtask:
    name: \Pleiades\Cleanup
    execute: C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe
    arguments: -NoProfile -File C:\ops\cleanup.ps1
    trigger: daily
    at: "02:30"
```

Is the backup task healthy:

```yaml
- name: Backup task
  win.schtask:
    name: \Pleiades\Backup
    state: query
  register: backup
```

