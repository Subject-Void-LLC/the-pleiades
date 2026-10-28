---
status: beta
---

# virt.cml.lab.start

Starts a CML lab's nodes and waits until they boot.

Starts each of the lab's nodes (or those nodes names) that is not running, then waits until every one of them reports BOOTED, up to timeout. A node already running is left alone, and a lab whose nodes all run is unchanged. Its undo is virt.cml.lab.stop of exactly the nodes this task started, so a rollback never stops a node someone else started. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads which nodes it would start and changes nothing.

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
| `timeout` | `int` | no | `900` | Seconds to wait for every node started to report BOOTED. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `started` | `list` | always | The labels of the nodes this task moved from stopped to started, exactly the nodes its undo stops. A start the controller refused partway lists only the nodes it started before the refusal. A check lists the nodes a run would start. |
| `already_running` | `list` | always | The labels of the chosen nodes already queued, started or booted, which the task left alone and its undo never stops. |
| `lab_id` | `string` | always | The lab's id. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that started nodes emits virt.cml.lab.stop naming exactly those nodes, kept when the wait for them to boot fails, so a rollback never stops a node someone else started. Stopping them does not undo what they did while they ran.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.lab.stop`

## Examples

Start a lab:

```yaml
- name: Start the CVD lab
  virt.cml.lab.start:
    lab: cvd
    timeout: 1200
```

