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
| Engine version | `>=1.0.0` |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `reply` | `string` | always | The trimmed value the remote command echoed back. |

## Examples

Check a device is reachable over SSH:

```yaml
- name: Ping the device
  fqcn: net.ssh.ping
  register: reachability
```

