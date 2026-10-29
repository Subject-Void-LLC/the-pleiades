---
status: beta
---

# net.ovs.port

Makes a port exist on an Open vSwitch bridge with the VLAN tag given, or not exist.

Reads the port through ovs-vsctl and changes only what differs. Its undo puts back what it found: a port it added is removed, one it removed is added back on the bridge it was on, and a tag it changed is set back.

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
| `bridge` | `string` | yes | - | The bridge the port belongs on. |
| `name` | `string` | yes | - | The port, usually an interface name (eth1). |
| `state` | `string` | no | `present` | present or absent. **Choices:** `present`, `absent`. |
| `tag` | `int` | no | - | An access VLAN, 1 to 4094; 0 removes the tag. Left unset, an existing port keeps its own. |
| `insecure_skip_host_key_verify` | `bool` | no | `false` | Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the port exists now. |
| `tag` | `int` | when it exists | Its access VLAN now, 0 for none. |
| `inverse` | `dict` | when something changed | The undo. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo puts back what it found: an added port is removed, a removed one added back on its bridge with its tag (partial when it carried more, such as an interface type or trunks), a moved one moved back, and a changed tag set back.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `net.ovs.bridge`

## Examples

A port on br0:

```yaml
- name: h1's port is on br0
  net.ovs.port:
    bridge: br0
    name: eth2
```

