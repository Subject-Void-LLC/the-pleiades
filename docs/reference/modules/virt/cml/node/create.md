---
status: beta
---

# virt.cml.node.create

Creates a node in a CML lab, optionally seeded with an inventory device's login and address.

Makes a node from a node definition, with the definition's interfaces, stopped. With device, the node stands for that inventory device (a cisco_router or cisco_switch; only an IOS or IOS XE definition): its day-0 configuration sets the hostname to the label, puts the device's host on management_interface in the Mgmt-intf VRF with a default route to management_gateway, enables SSH, and creates the device's login as "username <user> privilege 15 secret 9 <hash>" (and "enable secret 9" when the credential has an enable password), from the device's stored credential. The passwords never reach the runbook or the task's output: the method is handed type 9 hashes only. The hashes are in the node's configuration, which the controller keeps and anyone who can read the lab can read, so seed a node only from a device whose credential exists only in the lab. The console line logs an idle session out after five minutes. configuration adds lines after those. A label the lab already uses is refused, so a second run is a refusal rather than a second node. Its undo is virt.cml.node.delete of the node's id. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads the lab, the definition and the label and changes nothing.

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
| `label` | `string` | yes | - | The node's label in the lab, also its hostname when device is set. |
| `definition` | `string` | yes | - | The node definition's id, e.g. iol-xe. |
| `device` | `string` | no | - | The inventory device the node stands for: its host becomes the management address and its stored login is seeded as IOS secrets. |
| `management_interface` | `string` | no | `Ethernet0/0` | The interface that carries the management address. |
| `management_prefix` | `int` | no | `24` | The management address's prefix length. |
| `management_gateway` | `string` | no | - | The management network's gateway, the Mgmt-intf VRF's default route. Required with device. |
| `configuration` | `string` | no | - | Day-0 configuration lines added after the seeded ones, or the whole day-0 configuration without device. Never put a password here: use device. |
| `interfaces` | `int` | no | - | The least number of physical interfaces the node has. A definition gives a few (iol-xe four, Ethernet0/0 to Ethernet0/3); more are added slot by slot, named as the definition names them (iol-xe's fifth is Ethernet1/0). Up to 128, and no more than the definition allows, which the controller enforces. |
| `x` | `int` | no | `0` | The node's place on the lab's canvas, left to right. |
| `y` | `int` | no | `0` | The node's place on the lab's canvas, top to bottom. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | on change | The new node's id. |
| `interfaces` | `list` | on change | Its interfaces' labels, in slot order. |
| `management_address` | `string` | when device is set | The address the node's management interface was given. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that made a node emits virt.cml.node.delete naming its lab's id and its own, so a rollback removes exactly that node.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.link.create`
- `virt.cml.node.delete`
- `virt.cml.lab.start`

## Examples

A core router standing for an inventory device:

```yaml
- name: Make CORE1
  virt.cml.node.create:
    lab: cvd
    label: CORE1
    definition: iol-xe
    device: CORE1
    management_gateway: 10.10.20.254
    x: 0
    y: 0
```

