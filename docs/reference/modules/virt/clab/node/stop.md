---
status: beta
---

# virt.clab.node.stop

Makes one lab node stopped, if it is not already.

Reads the node's state first and acts only on a node that is running or paused, so a node already stopped is left alone and reported unchanged. Its undo is virt.clab.node.start, recorded for a node that was running. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `state` | `string` | always | The node's state after: running, exited or paused. |
| `name` | `string` | always | The node's container name. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

Its undo is virt.clab.node.start, recorded for a node that was running, and partial for one that was paused, which a start does not restore.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.node.start`
- `virt.clab.lab.info`

## Examples

Node stop:

```yaml
- name: r1 stop
  virt.clab.node.stop:
    target: clab-api
    lab: nclab
    node: r1
```

