---
status: beta
---

# virt.cml.node.delete

Stops, wipes and deletes one node of a CML lab.

Stops the node, wipes its disks and removes it with its links. A node that does not exist is unchanged. It cannot be undone: the node's disk and configuration are gone. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check reads whether the node exists and changes nothing.

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
| `node` | `string` | yes | - | The node, by label or id. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `node_id` | `string` | on change | The id of the node removed. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

The node's disk and configuration are wiped before it is removed, and nothing keeps them, so nothing can put it back as it was.

Note that no rollback engine reads this yet. What exists today is the recording, which has to happen during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.cml.node.create`

## Examples

Remove a node:

```yaml
- name: Remove BR3
  virt.cml.node.delete:
    lab: cvd
    node: BR3
```

