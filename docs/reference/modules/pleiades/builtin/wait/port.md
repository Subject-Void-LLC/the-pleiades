---
status: beta
---

# pleiades.builtin.wait.port

Waits for a TCP port on the target to start (or stop) accepting connections.

Holds the runbook until a TCP port accepts a connection, or until it stops accepting one. The check runs ON THE DEVICE, over the SSH connection the task already holds, which is where Ansible's wait_for runs it and is the only place it answers the useful question: a service bound to 127.0.0.1 is invisible from the runner, and a firewall between the runner and the device would report on the path rather than on the service. Running there costs a prerequisite, stated rather than assumed: the device needs python3 or bash, checked once before polling starts and refused by name when neither is present. python3 is preferred because its exit status is this method's own to define; bash is the fallback because it needs no package installed, using its /dev/tcp redirection, and a bash built without that feature is detected and reported rather than mistaken for a closed port. nc is deliberately not used: its flags and exit statuses differ between the openbsd, traditional and busybox builds, so a wrong answer from it could not be told apart from a real one. Never reports changed: waiting observes, it does not act.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `NetworkAddressableCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Not supported: a check run names this task as unchecked, since what a wait waits for is usually an earlier task's change, which a check never makes, so a check would wait out its timeout and fail where the real run succeeds |
| Engine version | `>=1.0.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `port` | `int` | yes | - | The TCP port to wait on, 1 to 65535. Write it unquoted: a quoted "8080" is text rather than a number and is refused by name. |
| `host` | `string` | no | `127.0.0.1` | The address to connect to, resolved and dialed ON THE DEVICE. The default is loopback, which means the device itself, and is what makes this method see a service bound only to 127.0.0.1. |
| `timeout` | `int` | no | `300` | How many seconds to wait in total before giving up, at most 86400. Measured from the start of the task, so delay, the SSH connection and the tool check all come out of this budget, exactly as Ansible measures it. Running out is an error, not a quiet pass. |
| `delay` | `int` | no | `0` | How many seconds to wait before the first check, at most 86400. Useful when a service is known to accept connections briefly before it is really ready. |
| `sleep` | `int` | no | `1` | How many seconds to wait between checks, at least 1 and at most 86400. The sleep happens on the runner, not on the device, so it costs the device nothing. Zero is refused because it would open SSH sessions in a tight loop. |
| `state` | `string` | no | `started` | Wait for the port to start accepting connections, or to stop. Ansible's present and absent describe a path and its drained describes connection counts, so all three are refused here rather than silently read as started. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `elapsed` | `int` | always | Whole seconds spent waiting, including the delay. Recorded on a timeout too, so a failed task still says how long it held. |
| `port` | `int` | always | The port that was waited on. |
| `host` | `string` | always | The address that was dialed from the device, filled in with the default when the task did not name one. |
| `state` | `string` | always | The state that was waited for, either started or stopped. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method only observes: it opens a TCP connection from the device and reports whether it succeeded, so the device is in exactly the state it would have been in had the task never run. There is nothing to undo, and an inverse that waited again would be work the forward run never did.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `wait.path`
- `wait.search`
- `exec.command`

## Examples

Wait for a database to come back after a restart:

```yaml
- name: Wait for postgres to accept connections
  pleiades.builtin.wait.port:
    port: 5432
    timeout: 120
```

Wait for a port to be released before rebinding it:

```yaml
- name: Wait for the old listener to go away
  pleiades.builtin.wait.port:
    port: 8080
    state: stopped
    timeout: 60
```

Give a service a head start, then poll slowly:

```yaml
- name: Wait for the API on its private address
  pleiades.builtin.wait.port:
    host: 10.0.0.7
    port: 443
    delay: 10
    sleep: 5
```

