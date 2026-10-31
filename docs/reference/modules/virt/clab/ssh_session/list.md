---
status: beta
---

# virt.clab.ssh_session.list

Lists the temporary SSH sessions the server holds.

Returns each session's port, lab, node, account and expiry. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `all` | `bool` | no | `false` | Every session, not only this login's. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `sessions` | `list of dict` | always | Each session: port, lab_name, node_name, username, expiration, created. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.node.ssh_access`

## Examples

Lists the temporary SSH sessions the server holds.:

```yaml
- name: Open doors
  virt.clab.ssh_session.list:
    target: clab-api
```

