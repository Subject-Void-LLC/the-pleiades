---
status: beta
---

# virt.clab.lab.links

Lists a lab's links with each end's interface, address and state.

Narrow it to the links one node has with node. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `lab` | `string` | yes | - | The lab's name, as containerlab deployed it. |
| `node` | `string` | no | - | Only the links this node has. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `links` | `list of dict` | always | Each link: type, and its two ends (a and b) with node, container, interface, mac, mtu and state. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.interfaces`

## Examples

Lists a lab's links with each end's interface, address and state.:

```yaml
- name: The lab's links
  virt.clab.lab.links:
    target: clab-api
    lab: nclab
```

