---
status: beta
---

# wait.connection

Waits until the target answers a command over its own connection, SSH or WinRM.

Tries the device over the connection its other tasks use (WinRM for a device reached that way, such as a Windows server, and SSH for any other) until a command runs there, and fails when the timeout runs out first. It is Ansible's wait_for_connection: the task to put after one that starts or restarts a machine, before the tasks that need it. Each try logs in with the device's credential and runs a command that does nothing, so a machine whose port answers before its account can log in is not taken for ready. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay is spent out of the timeout rather than added to it, as wait_for_connection does. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetworkAddressableCapable` |
| Transports | `ssh`, `winrm` |
| Requires elevation | no |
| Check mode | Not supported: a check run names this task as unchecked, since what it waits for is usually a machine an earlier task starts or restarts, which a check never does, so a check would wait out its timeout and fail where the real run succeeds |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `timeout` | `int` | no | `600` | How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0. |
| `delay` | `int` | no | `0` | How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of. |
| `sleep` | `int` | no | `5` | How many seconds to wait between tries. Must be more than 0. |
| `connect_timeout` | `int` | no | `20` | How many seconds one try may take before it counts as failed. Must be more than 0. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, on a device reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `transport` | `string` | always | The connection that answered: ssh or winrm. |
| `diff` | `dict` | always | The connection that answered. Both halves are identical, since a wait changes nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method waits for a device to answer and changes nothing on it, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.path`
- `virt.vbox.vm.start`
- `pleiades.builtin.wait.port`

## Examples

Wait for a new Windows VM's first boot:

```yaml
- name: Wait for the Windows lab VM to let its Administrator in
  wait.connection:
    timeout: 1800
    sleep: 15
```

Wait out a reboot:

```yaml
- name: Give the machine time to go down, then wait for it
  wait.connection:
    delay: 30
    timeout: 600
```

