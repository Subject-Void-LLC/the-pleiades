---
status: beta
---

# virt.cml.lab.delete

Stops, wipes and deletes a CML lab.

Stops every node, wipes every node's disks and removes the lab. A lab that does not exist is unchanged. It cannot be undone: every node's disk and configuration is gone, which is why it is a method of its own and never part of another. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the lab exists and changes nothing.

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

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `lab_id` | `string` | on change | The id of the lab removed. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

Every node's disk and configuration is wiped before the lab is removed, and nothing keeps them, so nothing can put the lab back as it was.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.lab.create`

## Examples

Remove a lab:

```yaml
- name: Remove the CVD lab
  virt.cml.lab.delete:
    lab: cvd
```

