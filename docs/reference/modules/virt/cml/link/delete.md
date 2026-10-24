---
status: beta
---

# virt.cml.link.delete

Removes a link from a CML lab.

Removes the link named by link, or the one between a's a_interface and b's b_interface (one way or the other, never both). A link that does not exist is unchanged. Its undo is virt.cml.link.create of the same two interfaces. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the link exists and changes nothing.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `CMLAPICapable` |
| Transports | `https` |
| Requires elevation | no |
| Runs | in the host process (the CLI or a Runner); acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `lab` | `string` | yes | - | The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id. |
| `link` | `string` | no | - | The link's id. |
| `a` | `string` | no | - | One node, by label or id, with a_interface, b and b_interface. |
| `a_interface` | `string` | no | - | Its interface, by label. |
| `b` | `string` | no | - | The other node, by label or id. |
| `b_interface` | `string` | no | - | Its interface, by label. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | on change | The id of the link removed. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that removed a link emits virt.cml.link.create of the same two interfaces, each node by its id and each interface by its label; one that found no such link emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.link.create`

## Examples

Cut a branch's uplink:

```yaml
- name: Cut BR1's link to CORE1
  virt.cml.link.delete:
    lab: cvd
    a: CORE1
    a_interface: Ethernet0/1
    b: BR1
    b_interface: Ethernet0/1
```

