---
status: beta
---

# net.ssh.ping

Opens a real SSH connection to the target and echoes a value back, to prove reachability.

Dials the device's SSHTransportCapable host and port, authenticates with the credential the Controller attached to this dispatch, and runs a trivial, read-only remote command that echoes params.data (default "pong") back. Never reports changed: a connectivity check does not alter device state.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `SSHTransportCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=1.0.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `reply` | `string` | always | The trimmed value the remote command echoed back. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

This method opens a connection and echoes a value back. It changes nothing on the device, so there is nothing to undo; that is the same fact that makes it report changed: false.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## Examples

Check a device is reachable over SSH:

```yaml
- name: Ping the device
  net.ssh.ping:
  register: reachability
```

