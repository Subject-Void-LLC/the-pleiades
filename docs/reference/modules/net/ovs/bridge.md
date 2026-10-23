---
status: beta
---

# net.ovs.bridge

Makes an Open vSwitch bridge exist with the datapath given, or not exist.

Reads the bridge through ovs-vsctl and changes only what differs. Its undo puts back what it found: a bridge it created is removed, a datapath it changed is set back. Removing a bridge also removes its ports, which its undo does not recreate, so that undo is partial and says so.

## Attributes

|  |  |
| --- | --- |
| Capabilities | `OpenVSwitchCapable`, `CommandExecCapable` |
| Transports | `ssh` |
| Requires elevation | no |
| Runs | on or against the target device; acts on its target device |
| Check mode | Supported: reports what it would change and changes nothing |
| Engine version | `>=0.2.0` |

## Parameters

| Name | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `name` | `string` | yes | - | The bridge. |
| `state` | `string` | no | `present` | present or absent. |
| `datapath_type` | `string` | no | - | system (the kernel datapath) or netdev (userspace). Left unset, an existing bridge keeps its own and a new one gets Open vSwitch's default. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the bridge exists now. |
| `datapath_type` | `string` | when it exists | Its datapath now. |
| `inverse` | `dict` | when something changed | The undo. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo puts back what it found: a created bridge is removed, a changed datapath set back, and a removed bridge recreated without its ports or settings such as a fail mode or a controller, which that undo says by being partial.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ovs.port`

## Examples

A userspace bridge:

```yaml
- name: br0 exists
  net.ovs.bridge:
    name: br0
    datapath_type: netdev
```

