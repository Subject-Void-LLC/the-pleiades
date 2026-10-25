---
status: beta
---

# virt.cml.link.create

Links two nodes' interfaces in a CML lab.

Links interface a_interface of node a to interface b_interface of node b. The same link already there is unchanged; an interface already linked to anything else is refused. Its undo is virt.cml.link.delete of the link's id. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads both interfaces and changes nothing.

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
| `a` | `string` | yes | - | One node, by label or id. |
| `a_interface` | `string` | yes | - | Its interface, by label, e.g. Ethernet0/1. |
| `b` | `string` | yes | - | The other node, by label or id. |
| `b_interface` | `string` | yes | - | Its interface, by label. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | always | The link's id. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that made a link emits virt.cml.link.delete naming its lab's id and its own; one that found the same link already there emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.link.delete`
- `virt.cml.node.create`

## Examples

Link a branch to a core:

```yaml
- name: Link BR1 to CORE1
  virt.cml.link.create:
    lab: cvd
    a: CORE1
    a_interface: Ethernet0/1
    b: BR1
    b_interface: Ethernet0/1
```

