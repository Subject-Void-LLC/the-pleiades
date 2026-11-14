---
status: beta
---

# wait.netconf

Waits until the target's NETCONF listener lets its account in and sends its hello.

Tries the device's NETCONF listener, on its netconf_port (830 unless set), until a NETCONF session opens there, and fails when the timeout runs out first. It is the task to put between one that starts a network device and the first net.netconf.* task: a device can report booted, and answer SSH on its own port, while its NETCONF listener still refuses connections (a Catalyst 8000V's does for minutes after its lab reports it booted). Each try connects to the listener, completes the SSH handshake, logs in afresh with the device's credential, opens the netconf subsystem and reads the server's hello, then closes the session, so a session kept open from an earlier task is never taken for a login. The listener's host key is checked against known_hosts before any credential is sent, and that check cannot be skipped: a key known_hosts does not hold ends the wait at once, naming the trust-host command that trusts the listener (the key is seen only once the listener answers, so on a node made new the first wait ends there, with the listener up), and so does a key that differs from the one known_hosts holds, or a login the device refuses, since none of these changes by being asked again and a password sent at every try can lock the account. A listener that does not answer, refuses the connection, declines the netconf subsystem, or sends no hello within connect_timeout is tried again until the timeout. A failed try is not an error, since it is what the task waits to see stop; the last one is quoted when the timeout ends the wait. The delay, backoff and steady parameters work as wait.connection's do. It changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetconfCapable`, `SSHTransportCapable` |
| Transports | `netconf` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since what it waits for is usually a device an earlier task starts or restarts, which a check never does, so a check would wait out its timeout and fail where the real run succeeds |
| Absence | No exists return: it is a wait: failing at its timeout is its purpose |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `timeout` | `int` | no | `600` | How many seconds to wait in total before giving up, counted from the start of the task, so the delay comes out of it. Must be more than 0. |
| `delay` | `int` | no | `0` | How many seconds to wait before the first try. Must be shorter than the timeout, which it is spent out of. |
| `sleep` | `int` | no | `5` | How many seconds to wait between tries, or with backoff: exponential before the second try, the gap doubling after each failed one. Must be more than 0. |
| `backoff` | `string` | no | `fixed` | How the gap between failed tries grows: fixed keeps it at sleep; exponential doubles it after each failed try, up to max_sleep, each gap a random point in its upper half. **Choices:** `fixed`, `exponential`. |
| `max_sleep` | `int` | no | `60` | With backoff: exponential, the longest gap between tries, in seconds. Not less than sleep; refused with backoff: fixed, where it would do nothing. |
| `steady` | `int` | no | `1` | How many tries in a row must open a session before the listener counts as ready. Each is its own login. Must be at least 1, and the streak must fit in the timeout after the delay. |
| `steady_interval` | `int` | no | `10` | With steady above 1, the seconds between the answering tries. Must be more than 0; refused with steady 1, where it would do nothing. |
| `connect_timeout` | `int` | no | `20` | How many seconds one try may take, from the connection to the hello, before it counts as failed. Must be more than 0. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `elapsed` | `int` | always | How many whole seconds the task waited, including the delay. |
| `port` | `int` | always | The port of the NETCONF listener that answered. |
| `tries` | `int` | always | How many tries the task made, answered or not. |
| `failures` | `int` | always | How many of those tries did not answer. |
| `diff` | `dict` | always | The port that answered. Both halves are identical, since a wait changes nothing. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method waits for a device's NETCONF listener to answer and changes nothing on it, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.connection`
- `net.netconf.config`
- `virt.cml.lab.start`

## Examples

Wait for a router's NETCONF server after it boots:

```yaml
- name: Wait for the router's NETCONF listener to let its account in
  wait.netconf:
    timeout: 600
    sleep: 10
```

