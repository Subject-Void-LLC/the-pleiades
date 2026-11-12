---
status: beta
---

# virt.clab.lab.validate

Validates a lab's on-disk topology without deploying it.

Asks containerlab to parse the topology file the server holds for the lab and reports whether it is valid; one that is not valid fails the task. path names another file on the server. A lab the server holds no topology file for (or no file at path) is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. It only reads, so a check is the same call. The target is the containerlab API server, a generic_http device that onboarding recognised (it declares ContainerlabAPICapable); its login is the device's stored credential, with http_auth basic, and neither the password nor the token it gets is logged, returned or journaled.

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
| `path` | `string` | no | - | A topology file on the server, when it is not the lab's own. |

## Returns

| Name | Type | Returned | Description |
| --- | --- | --- | --- |
| `exists` | `bool` | always | Whether the server holds the topology file. |
| `valid` | `bool` | when it exists | Whether the topology is valid. |
| `nodes` | `int` | when it exists | How many nodes. |
| `links` | `int` | when it exists | How many links. |
| `error` | `string` | when invalid | containerlab's validation error. |

## Undoing this

**Cannot be undone.** This method never records a reversing instruction, so a rollback reaching a task that used it stops rather than guessing.

It only reads the server, so there is nothing to undo.

`pleiades rollback` and the Controller's rollback route replay a recorded instruction. It is recorded during the forward run because the values an undo needs are gone once the change is applied.

## See also

- `virt.clab.lab.apply`

## Examples

Validates a lab's on-disk topology without deploying it.:

```yaml
- name: Validate it first
  virt.clab.lab.validate:
    target: clab-api
    lab: nclab
```

