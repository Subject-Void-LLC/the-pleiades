---
status: beta
---

# win.reboot

Restarts a Windows host over WinRM and waits until it is back, as ansible.windows.win_reboot does.

Reads the host's last boot time, has shutdown.exe restart it pre_reboot_delay seconds later (closing applications that do not exit, and recorded as a planned restart for maintenance), then waits until the host answers over WinRM again with a later boot time, which is what shows it restarted rather than only answered, and then post_reboot_delay seconds more. reboot_timeout bounds the wait from the moment the restart is asked for; a try the host refuses, or does not answer within connect_timeout, is made again five seconds later. A restart already scheduled on the host is cancelled and replaced by this one. A run always reports a change, and a restart cannot be undone. A check reads the boot time and sends nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `WinRMCapable` |
| Transports | `winrm` |
| Requires elevation | yes |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `reboot_timeout` | `int` | no | `600` | How many seconds the host may take to come back, from the moment the restart is asked for. |
| `pre_reboot_delay` | `int` | no | `2` | How many seconds shutdown.exe waits before restarting, at least 0 and at most 600. |
| `post_reboot_delay` | `int` | no | `0` | How many seconds to wait once the host is back, for services that start after WinRM does. |
| `msg` | `string` | no | `Reboot initiated by The Pleiades` | The message shutdown.exe shows signed-in users and records, at most 512 characters on one line. |
| `connect_timeout` | `int` | no | `20` | How many seconds one try to reach the host may take. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `rebooted` | `bool` | always | Whether the host restarted: true on a run, false in a check. |
| `elapsed` | `int` | on a run | How many seconds the restart took, from asking for it to the host being back. |
| `previous_boot_time` | `string` | always | When the host last booted before this task, in UTC (RFC 3339). |
| `boot_time` | `string` | on a run | When the host booted this time, in UTC (RFC 3339). |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A restart cannot be taken back: once the host has restarted there is nothing to put back, so no undo is recorded.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `win.capability.install`
- `win.feature.install`
- `wait.connection`

## Examples

Restart after a change that needs it:

```yaml
- name: Install Server Core's application compatibility feature
  win.capability.install:
    name: ServerCore.AppCompatibility~~~~0.0.1.0
  register: appcompat

- name: Restart to finish it
  win.reboot:
    reboot_timeout: 900
  when:
    - result.appcompat.reboot_required
```

