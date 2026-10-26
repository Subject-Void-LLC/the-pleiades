---
status: beta
---

# virt.cml.link.start

Starts a stopped link in a CML lab, as if plugged in again.

Starts the link named by link, or the one between a's a_interface and b's b_interface, so it carries traffic again. A link already started is unchanged. Its undo is virt.cml.link.stop of the same link. The task's target is a cml_controller device; the method runs on this machine and talks to the controller's REST API. A check reads the link's state and changes nothing.

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
| `a` | `string` | no | - | One node, by label or id, with a_interface, b and b_interface, naming the link by its ends. |
| `a_interface` | `string` | no | - | Its interface, by label. |
| `b` | `string` | no | - | The other node, by label or id. |
| `b_interface` | `string` | no | - | Its interface, by label. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | always | The link's id. |
| `state` | `string` | always | The link's state before the task: STARTED, STOPPED or DEFINED_ON_CORE. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that changed the link's state emits virt.cml.link.stop of the same link, by its lab's id and its own; one that found the link already so emits nothing.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.link.stop`
- `virt.cml.lab.start`

## Examples

Restore a link a test cut:

```yaml
- name: Restore CORE1's WAN link
  virt.cml.link.start:
    lab: cvd
    a: CORE1
    a_interface: Ethernet0/1
    b: WAN1
    b_interface: Ethernet0/0
```

