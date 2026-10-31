---
status: beta
---

# virt.clab.netem.reset

Clears the netem impairments on a lab node's interface, if it has any.

Reads the interface first and acts only when it has an impairment, so a clean interface is reported unchanged. Its undo is virt.clab.netem.set with the settings it found. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `node` | `string` | yes | - | The node's name in the lab (n1), or its container's name (clab-<lab>-n1). |
| `interface` | `string` | yes | - | The interface (eth1), or its alias. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `interfaces` | `list of dict` | always | The node's impaired interfaces after. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo is virt.clab.netem.set with the settings it found.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.netem.set`

## Examples

Heal the link:

```yaml
- name: r1 eth1 is clean
  virt.clab.netem.reset:
    target: clab-api
    lab: nclab
    node: r1
    interface: eth1
```

