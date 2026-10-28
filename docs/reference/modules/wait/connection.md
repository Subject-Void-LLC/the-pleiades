---
status: beta
---

# wait.connection

Waits until the target answers a command over its own connection, SSH or WinRM.

Tries the device over the connection its other tasks use (WinRM for a device reached that way, such as a Windows server, and SSH for any other) until a command runs there, and fails when the timeout runs out first. It is Ansible's wait_for_connection: the task to put after one that starts or restarts a machine, before the tasks that need it. Each try logs in afresh with the device's credential and runs a command that does nothing, so a machine whose port answers before its account can log in is not taken for ready, and a session kept open from an earlier task is never taken for a login. A network device's CLI is not a shell with a command that does nothing, so for one that declares CiscoIOSCapable, or a cli_prompt, a try opens its terminal and waits for its prompt instead. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay is spent out of the timeout rather than added to it, as wait_for_connection does. With backoff: exponential the gap after each failed try doubles from sleep up to max_sleep, each gap a random point in its upper half so machines restarted together do not try in step. With steady above 1 the machine must answer that many tries in a row, steady_interval apart, which a machine just resumed or restarted that answers once and then refuses for a while does not; a refusal mid-streak starts the streak, and the backoff, again. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetworkAddressableCapable` |
| Transports | `ssh`, `winrm` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since what it waits for is usually a machine an earlier task starts or restarts, which a check never does, so a check would wait out its timeout and fail where the real run succeeds |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `timeout` | `int` | no | `600` | How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0. |
| `delay` | `int` | no | `0` | How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of. |
| `sleep` | `int` | no | `5` | How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0. |
| `backoff` | `string` | no | `fixed` | How the gap between failed tries grows: fixed keeps it at sleep, as wait_for_connection does; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half. **Choices:** `fixed`, `exponential`. |
| `max_sleep` | `int` | no | `60` | With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing. |
| `steady` | `int` | no | `1` | How many tries in a row must answer before the machine counts as ready. Each is its own login. Must be at least 1, and the streak must fit in the timeout after the delay. |
| `steady_interval` | `int` | no | `10` | With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing. |
| `connect_timeout` | `int` | no | `20` | How many seconds one try may take before it counts as failed. Must be more than 0. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task, on a device reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `transport` | `string` | always | The connection that answered: ssh or winrm. |
| `tries` | `int` | always | How many tries the task made, answered or not. |
| `failures` | `int` | always | How many of those tries did not answer. |
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

Wait for a resumed VM that keeps answering:

```yaml
- name: Wait until the resumed VM lets its account in three times running
  wait.connection:
    timeout: 900
    backoff: exponential
    sleep: 2
    max_sleep: 60
    steady: 3
    steady_interval: 10
```

