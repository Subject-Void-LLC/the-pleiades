---
status: beta
---

# virt.cml.lab.create

Creates an empty CML lab with a title.

Makes a lab with no nodes. A title a lab already has is refused, since CML lets two labs share one and every later task names its lab by title; a second run of the same task is therefore a refusal, not a second lab. Its undo is virt.cml.lab.delete of the lab's id, so a rollback removes exactly the lab this task made. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the title is free and changes nothing.

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
| `title` | `string` | yes | - | The new lab's title. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `id` | `string` | on change | The new lab's id. |
| `title` | `string` | always | The lab's title. |

## Undoing this

**Can be undone.** A run that changes something records the instruction that reverses it, as an `inverse` stat holding the method to call and the parameters to call it with, resolved from the state this run actually found. A run that changed nothing records no instruction, which is how it says that undoing it means doing nothing.

A run that made a lab emits virt.cml.lab.delete naming its id, so a rollback removes exactly that lab and whatever was later built in it.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.node.create`
- `virt.cml.lab.delete`

## Examples

Make a lab:

```yaml
- name: Make the CVD lab
  virt.cml.lab.create:
    title: cvd
```

