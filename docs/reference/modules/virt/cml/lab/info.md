---
status: beta
---

# virt.cml.lab.info

Reports one CML lab: its nodes, their interfaces and its links.

Reads one lab and changes nothing. A lab that does not exist is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check is the same read.

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
| `exists` | `bool` | always | Whether the controller holds the lab. |
| `id` | `string` | when exists | The lab's id. |
| `title` | `string` | when exists | The lab's title. |
| `state` | `string` | when exists | DEFINED_ON_CORE, STARTED or STOPPED. |
| `nodes` | `list` | when exists | Each node as id, label, definition, state and interfaces (each as label and connected). |
| `links` | `list` | when exists | Each link as id, a, a_interface, b and b_interface, by node label and interface label. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the controller.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.lab.list`
- `virt.cml.lab.create`

## Examples

Read a lab:

```yaml
- name: Read the CVD lab
  virt.cml.lab.info:
    lab: cvd
  register: lab
```

