---
status: beta
---

# virt.cml.lab.stop

Stops a CML lab's nodes.

Stops each of the lab's nodes (or those nodes names) that is running, keeping their disks. A node already stopped is left alone. Its undo is virt.cml.lab.start of exactly the nodes this task stopped. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads which nodes it would stop and changes nothing.

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
| `nodes` | `list` | no | - | The nodes to act on, by label or id; every node of the lab when empty. A rollback sets it to exactly the nodes the run changed. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `stopped` | `list` | always | The labels of the nodes this task stopped. |
| `lab_id` | `string` | always | The lab's id. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that stopped nodes emits virt.cml.lab.start naming exactly those nodes, kept when a stop fails partway, so a rollback starts only what this task stopped.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.lab.start`

## Examples

Stop one node:

```yaml
- name: Take CORE1 down to prove the second hub carries the branches
  virt.cml.lab.stop:
    lab: cvd
    nodes:
      - CORE1
```

