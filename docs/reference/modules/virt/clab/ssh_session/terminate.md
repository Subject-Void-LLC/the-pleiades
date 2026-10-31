---
status: beta
---

# virt.clab.ssh_session.terminate

Closes a temporary SSH session on the server, if it is open.

Terminates the session on the port given. A session that is not open is reported unchanged. It cannot be undone: a closed door is opened again by virt.clab.node.ssh_access, which makes a new one. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `port` | `int` | yes | - | The session's port, as ssh_access returned it. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

A closed session is opened again by virt.clab.node.ssh_access, which makes a new one on another port.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.node.ssh_access`
- `virt.clab.ssh_session.list`

## Examples

Close a door:

```yaml
- name: Close it
  virt.clab.ssh_session.terminate:
    target: clab-api
    port: 2223
```

