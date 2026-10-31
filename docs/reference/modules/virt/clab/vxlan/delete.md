---
status: beta
---

# virt.clab.vxlan.delete

Deletes the VxLAN interfaces containerlab made with a prefix.

Removes every VxLAN interface on the server's host whose name starts with prefix. It cannot be undone: the tunnels' settings are not read first. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `ContainerlabAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Not supported: a check run names this task as unchecked, since which interfaces carry the prefix is not read first |
| Engine version | `>=0.3.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `prefix` | `string` | no | `vx-` | The interface-name prefix; vx- is what containerlab uses. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

The tunnels' settings are not read first, so they cannot be put back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.vxlan.create`

## Examples

Remove the tunnels:

```yaml
- name: No more tunnels
  virt.clab.vxlan.delete:
    target: clab-api
```

